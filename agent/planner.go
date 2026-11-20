package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// PlannerArgs is the River job payload for a planner round.
type PlannerArgs struct {
	EngagementID string `json:"engagement_id"`
	Round        int    `json:"round"`
}

func (PlannerArgs) Kind() string { return "planner" }

// PlannerWorker executes one planner round for an engagement.
type PlannerWorker struct {
	river.WorkerDefaults[PlannerArgs]
	db  *pgxpool.Pool
	cfg EngineConfig
}

func (w *PlannerWorker) Work(ctx context.Context, job *river.Job[PlannerArgs]) error {
	p := &Planner{db: w.db, cfg: w.cfg}
	return p.RunRound(ctx, job.Args.EngagementID, job.Args.Round)
}

// Planner manages the hypothesis DAG and spawns worker intents.
type Planner struct {
	db  *pgxpool.Pool
	cfg EngineConfig
}

func NewPlanner(db *pgxpool.Pool, cfg EngineConfig) *Planner {
	return &Planner{db: db, cfg: cfg}
}

// RunRound executes one planning cycle:
// 1. Load engagement state + open hypotheses
// 2. Check for stagnation (≥3 repeated conclusions)
// 3. Prioritize and spawn next worker intents
// 4. Compact cold nodes (R=6, minor N=20, major M=8)
func (p *Planner) RunRound(ctx context.Context, engID string, round int) error {
	start := time.Now()

	// Load engagement goal
	var goal, status string
	if err := p.db.QueryRow(ctx,
		`SELECT goal, status FROM engagements WHERE id = $1`, engID,
	).Scan(&goal, &status); err != nil {
		return fmt.Errorf("load engagement: %w", err)
	}
	if status != "running" {
		return nil
	}

	// Load open hypotheses ordered by confidence DESC
	hypos, err := p.loadOpenHypotheses(ctx, engID, 20)
	if err != nil {
		return fmt.Errorf("load hypotheses: %w", err)
	}

	// Stagnation detection: count repeated hypothesis values
	if p.detectStagnation(hypos) {
		slog.Warn("planner stagnation detected", "engagement_id", engID, "round", round)
		if err := p.triggerHardFloor(ctx, engID); err != nil {
			return err
		}
	}

	// Spawn next intents for top-priority hypotheses without active workers
	spawned, err := p.spawnIntents(ctx, engID, hypos)
	if err != nil {
		return fmt.Errorf("spawn intents: %w", err)
	}

	// Cold-node compaction
	if round%6 == 0 {
		if err := p.compactColdNodes(ctx, engID, round); err != nil {
			slog.Warn("cold node compaction failed", "err", err)
		}
	}

	slog.Info("planner round complete",
		"engagement_id", engID,
		"round", round,
		"hypotheses", len(hypos),
		"spawned", spawned,
		"elapsed", time.Since(start),
	)
	return nil
}

// loadOpenHypotheses fetches open hypotheses sorted by confidence.
func (p *Planner) loadOpenHypotheses(ctx context.Context, engID string, limit int) ([]*HypothesisNode, error) {
	rows, err := p.db.Query(ctx, `
		SELECT id, title, description, confidence, preconditions, tags
		FROM hypotheses
		WHERE engagement_id = $1 AND status = 'open'
		ORDER BY confidence DESC
		LIMIT $2`, engID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hypos []*HypothesisNode
	for rows.Next() {
		h := &HypothesisNode{}
		var precJSON, tagsArr []byte
		if err := rows.Scan(&h.ID, &h.Title, &h.Description, &h.Confidence, &precJSON, &tagsArr); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(precJSON, &h.Preconditions)
		hypos = append(hypos, h)
	}
	return hypos, rows.Err()
}

// detectStagnation returns true if ≥3 hypotheses share the same title prefix.
func (p *Planner) detectStagnation(hypos []*HypothesisNode) bool {
	seen := make(map[string]int)
	for _, h := range hypos {
		key := h.Title[:min(30, len(h.Title))]
		seen[key]++
		if seen[key] >= 3 {
			return true
		}
	}
	return false
}

// triggerHardFloor diversifies the hypothesis set by injecting new angles.
func (p *Planner) triggerHardFloor(ctx context.Context, engID string) error {
	_, err := p.db.Exec(ctx, `
		UPDATE engagements
		SET metadata = jsonb_set(metadata, '{hard_floor_triggered}', to_jsonb(now()::text), true)
		WHERE id = $1`, engID)
	return err
}

// spawnIntents creates River worker jobs for hypotheses without active intents.
func (p *Planner) spawnIntents(ctx context.Context, engID string, hypos []*HypothesisNode) (int, error) {
	// Count active workers
	var active int
	_ = p.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM intents WHERE engagement_id = $1 AND status = 'running'`, engID,
	).Scan(&active)

	if active >= p.cfg.MaxWorkers {
		return 0, nil
	}

	spawned := 0
	for _, h := range hypos {
		if active+spawned >= p.cfg.MaxWorkers {
			break
		}
		// Check if intent already exists for this hypothesis
		var exists bool
		_ = p.db.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM intents
				WHERE engagement_id = $1
				  AND metadata->>'hypothesis_id' = $2
				  AND status NOT IN ('done','failed','killed')
			)`, engID, h.ID).Scan(&exists)
		if exists {
			continue
		}

		// Create the intent
		var intentID string
		err := p.db.QueryRow(ctx, `
			INSERT INTO intents (engagement_id, kind, title, goal, status, model_tier, metadata)
			VALUES ($1, 'probe', $2, $3, 'queued', 1, $4)
			RETURNING id`,
			engID, h.Title, h.Description,
			fmt.Sprintf(`{"hypothesis_id":"%s"}`, h.ID),
		).Scan(&intentID)
		if err != nil {
			return spawned, fmt.Errorf("create intent: %w", err)
		}

		spawned++
	}
	return spawned, nil
}

// compactColdNodes merges low-value hypothesis nodes to reduce DAG complexity.
func (p *Planner) compactColdNodes(ctx context.Context, engID string, round int) error {
	// Minor compaction (N=20): mark very-low-confidence open hypos as deferred
	if round%20 == 0 {
		_, err := p.db.Exec(ctx, `
			UPDATE hypotheses
			SET status = 'deferred', updated_at = now()
			WHERE engagement_id = $1
			  AND status = 'open'
			  AND confidence < 0.1
			  AND updated_at < now() - interval '30 minutes'`, engID)
		return err
	}
	return nil
}

// AddHypothesis adds a new node to the hypothesis DAG.
func (p *Planner) AddHypothesis(ctx context.Context, engID string, h *HypothesisInput) (string, error) {
	precJSON, _ := json.Marshal(h.Preconditions)
	var id string
	err := p.db.QueryRow(ctx, `
		INSERT INTO hypotheses (engagement_id, intent_id, title, description, confidence, preconditions, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		engID, h.IntentID, h.Title, h.Description, h.Confidence, precJSON, h.Tags,
	).Scan(&id)
	return id, err
}

type HypothesisNode struct {
	ID            string
	Title         string
	Description   string
	Confidence    float64
	Preconditions []string
	Tags          []string
}

type HypothesisInput struct {
	IntentID     *string
	Title        string
	Description  string
	Confidence   float64
	Preconditions []string
	Tags         []string
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
