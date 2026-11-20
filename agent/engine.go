// Package agent implements the multi-agent orchestration engine backed by riverqueue/river.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/threatvector/arkex/llmpool"
	"github.com/threatvector/arkex/llmrec"
	"github.com/threatvector/arkex/sandbox"
)

// Engine orchestrates planner + worker loops via River distributed job queue.
type Engine struct {
	db          *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	planner     *Planner
	cfg         EngineConfig
}

type EngineConfig struct {
	MaxWorkers       int
	PlannerInterval  time.Duration
	GovernorInterval time.Duration
	TierModels       [3]string // Tier 1/2/3 model IDs
}

func DefaultConfig() EngineConfig {
	return EngineConfig{
		MaxWorkers:       4,
		PlannerInterval:  800 * time.Millisecond,
		GovernorInterval: 10 * time.Minute,
		TierModels: [3]string{
			"claude-haiku-5-5",   // Tier 1: fast/cheap
			"claude-sonnet-5-5",  // Tier 2: frontier reasoning
			"ollama/llama3.2",    // Tier 3: local uncensored
		},
	}
}

func New(db *pgxpool.Pool, cfg EngineConfig) (*Engine, error) {
	scratchDir := os.Getenv("ARKEX_SCRATCH_DIR")
	if scratchDir == "" {
		scratchDir = "/tmp/arkex-scratch"
	}
	sbMgr, err := sandbox.NewManager(os.Getenv("ARKEX_PROXY_ADDR"), scratchDir, os.Getenv("ARKEX_SANDBOX_NETWORK"))
	if err != nil {
		return nil, fmt.Errorf("sandbox manager: %w", err)
	}

	llmPool := llmpool.New(llmpool.DefaultProviders(
		os.Getenv("ANTHROPIC_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OLLAMA_URL"),
	))
	rec := llmrec.New(db)

	workers := river.NewWorkers()
	river.AddWorker(workers, &PlannerWorker{db: db, cfg: cfg})
	river.AddWorker(workers, &IntentWorker{db: db, pool: llmPool, rec: rec, sandbox: sbMgr, cfg: cfg})

	rc, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			"planner": {MaxWorkers: 1},
			"worker":  {MaxWorkers: cfg.MaxWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}

	return &Engine{
		db:          db,
		riverClient: rc,
		planner:     NewPlanner(db, cfg),
		cfg:         cfg,
	}, nil
}

// Start launches the River queue processor and planner periodic trigger.
func (e *Engine) Start(ctx context.Context) error {
	if err := e.riverClient.Start(ctx); err != nil {
		return fmt.Errorf("river start: %w", err)
	}
	slog.Info("arkex engine started", "max_workers", e.cfg.MaxWorkers)
	return nil
}

// Stop drains in-flight jobs and shuts down gracefully.
func (e *Engine) Stop(ctx context.Context) error {
	return e.riverClient.Stop(ctx)
}

// LaunchEngagement inserts a planner job for a new engagement.
func (e *Engine) LaunchEngagement(ctx context.Context, engID string) error {
	_, err := e.riverClient.Insert(ctx, PlannerArgs{EngagementID: engID}, &river.InsertOpts{
		Queue:    "planner",
		Priority: 1,
	})
	if err != nil {
		return fmt.Errorf("launch engagement: %w", err)
	}
	slog.Info("engagement launched", "engagement_id", engID)
	return nil
}

// SteerWork sends a steer directive to an active intent via metadata update.
func (e *Engine) SteerWork(ctx context.Context, intentID, directive string) error {
	_, err := e.db.Exec(ctx, `
		UPDATE intents
		SET metadata = jsonb_set(metadata, '{steer_directive}', to_jsonb($2::text), true),
		    updated_at = now()
		WHERE id = $1 AND status = 'running'`,
		intentID, directive,
	)
	return err
}

// KillWork immediately terminates an active intent.
func (e *Engine) KillWork(ctx context.Context, intentID string) error {
	tag, err := e.db.Exec(ctx, `
		UPDATE intents SET status = 'killed', finished_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('running','queued')`,
		intentID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("intent %s not running", intentID)
	}
	return nil
}

// GetEngagementStatus returns current engine state for an engagement.
func (e *Engine) GetEngagementStatus(ctx context.Context, engID string) (*EngagementStatus, error) {
	var s EngagementStatus
	s.EngagementID = engID

	err := e.db.QueryRow(ctx, `
		SELECT status, COALESCE(started_at, now()), COALESCE(finished_at, 'epoch'::timestamptz)
		FROM engagements WHERE id = $1`, engID,
	).Scan(&s.Status, &s.StartedAt, &s.FinishedAt)
	if err != nil {
		return nil, err
	}

	rows, err := e.db.Query(ctx, `
		SELECT id, kind, title, status, tokens_in + tokens_out AS tokens
		FROM intents WHERE engagement_id = $1 ORDER BY created_at DESC LIMIT 20`, engID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i IntentSummary
		if err := rows.Scan(&i.ID, &i.Kind, &i.Title, &i.Status, &i.Tokens); err != nil {
			return nil, err
		}
		s.Intents = append(s.Intents, i)
	}
	return &s, rows.Err()
}

type EngagementStatus struct {
	EngagementID string
	Status       string
	StartedAt    time.Time
	FinishedAt   time.Time
	Intents      []IntentSummary
}

type IntentSummary struct {
	ID     string
	Kind   string
	Title  string
	Status string
	Tokens int64
}
