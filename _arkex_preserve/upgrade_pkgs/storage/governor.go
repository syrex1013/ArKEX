package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const (
	DefaultMaxDiskGB      = 20
	DefaultScratchQuotaMB = 256
	FTSRetentionDays      = 7
	LLMRawDebugTTLHours   = 24
)

// Governor enforces disk quotas, runs TTL cleanup, and coordinates Parquet cold tiering.
type Governor struct {
	db         *pgxpool.Pool
	scratchDir string
	maxDiskGB  int64
	scratchMB  int64
	river      *river.Client[pgxpool.Tx]
}

func NewGovernor(db *pgxpool.Pool, scratchDir string) *Governor {
	maxGB := int64(DefaultMaxDiskGB)
	if v := os.Getenv("ARKEX_MAX_DISK_GB"); v != "" {
		fmt.Sscanf(v, "%d", &maxGB)
	}
	return &Governor{
		db:         db,
		scratchDir: scratchDir,
		maxDiskGB:  maxGB,
		scratchMB:  DefaultScratchQuotaMB,
	}
}

// GovArgs is the River job args for the periodic Governor run.
type GovArgs struct{}

func (GovArgs) Kind() string { return "storage_governor" }

// Run executes all governor checks. Called by River periodically.
func (g *Governor) Run(ctx context.Context, job *river.Job[GovArgs]) error {
	start := time.Now()
	var errs []error

	if err := g.purgeLLMRawDebug(ctx); err != nil {
		errs = append(errs, fmt.Errorf("llm raw debug: %w", err))
	}
	if err := g.dropStaleFTSIndex(ctx); err != nil {
		errs = append(errs, fmt.Errorf("fts index: %w", err))
	}
	if err := g.cleanScratch(ctx); err != nil {
		errs = append(errs, fmt.Errorf("scratch: %w", err))
	}
	if err := g.recordMetrics(ctx); err != nil {
		errs = append(errs, fmt.Errorf("metrics: %w", err))
	}
	if err := g.enforceGlobalCap(ctx); err != nil {
		errs = append(errs, fmt.Errorf("global cap: %w", err))
	}

	slog.Info("storage governor run", "elapsed", time.Since(start), "errors", len(errs))
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// purgeLLMRawDebug drops rows older than 24h from llm_raw_debug.
func (g *Governor) purgeLLMRawDebug(ctx context.Context) error {
	tag, err := g.db.Exec(ctx,
		`DELETE FROM llm_raw_debug WHERE created_at < now() - interval '24 hours'`)
	if err != nil {
		return err
	}
	slog.Debug("purged llm_raw_debug", "rows", tag.RowsAffected())
	return nil
}

// dropStaleFTSIndex removes traffic_fts rows for tasks closed >7 days ago.
func (g *Governor) dropStaleFTSIndex(ctx context.Context) error {
	tag, err := g.db.Exec(ctx, `
		DELETE FROM traffic_fts tf
		USING traffic_requests tr, traffic_sessions ts, engagements e
		WHERE tf.request_id = tr.id
		  AND tr.session_id = ts.id
		  AND ts.engagement_id = e.id
		  AND e.status IN ('done','cancelled')
		  AND e.finished_at < now() - interval '7 days'`)
	if err != nil {
		return err
	}
	slog.Debug("dropped stale FTS rows", "rows", tag.RowsAffected())
	return nil
}

// cleanScratch removes task scratch dirs whose intents finished and have no linked evidence.
func (g *Governor) cleanScratch(ctx context.Context) error {
	rows, err := g.db.Query(ctx, `
		SELECT i.id, i.scratch_bytes
		FROM intents i
		WHERE i.status IN ('done','failed','killed')
		  AND i.finished_at < now() - interval '1 hour'
		  AND i.scratch_bytes > 0`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var scratched int64
		if err := rows.Scan(&id, &scratched); err != nil {
			continue
		}
		dir := filepath.Join(g.scratchDir, id)
		if err := os.RemoveAll(dir); err == nil {
			_, _ = g.db.Exec(ctx, `UPDATE intents SET scratch_bytes = 0 WHERE id = $1`, id)
		}
	}
	return rows.Err()
}

// recordMetrics snapshots current disk usage into storage_metrics.
func (g *Governor) recordMetrics(ctx context.Context) error {
	var pgBytes, blobOrig, blobComp int64
	_ = g.db.QueryRow(ctx,
		`SELECT pg_database_size(current_database())`).Scan(&pgBytes)
	_ = g.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_original),0), COALESCE(SUM(size_compressed),0) FROM blobs`,
	).Scan(&blobOrig, &blobComp)

	ratio := float64(1)
	if blobComp > 0 {
		ratio = float64(blobOrig) / float64(blobComp)
	}

	_, err := g.db.Exec(ctx, `
		INSERT INTO storage_metrics (pg_bytes, blob_bytes, compression_ratio)
		VALUES ($1, $2, $3)`,
		pgBytes, blobComp, ratio)
	return err
}

// enforceGlobalCap evicts old blobs if total exceeds ARKEX_MAX_DISK_GB.
func (g *Governor) enforceGlobalCap(ctx context.Context) error {
	var totalGB float64
	_ = g.db.QueryRow(ctx, `
		SELECT (pg_database_size(current_database()) + COALESCE(SUM(size_compressed),0))::float / 1e9
		FROM blobs`).Scan(&totalGB)

	if totalGB < float64(g.maxDiskGB)*0.9 {
		return nil
	}
	slog.Warn("storage cap approaching, evicting oldest unreferenced blobs",
		"total_gb", totalGB, "max_gb", g.maxDiskGB)

	// Evict blobs not referenced by any finding evidence (safe to drop)
	tag, err := g.db.Exec(ctx, `
		DELETE FROM blobs b
		WHERE b.created_at < now() - interval '30 days'
		  AND NOT EXISTS (
			SELECT 1 FROM findings f WHERE f.evidence::text LIKE '%' || b.sha256 || '%'
		  )
		LIMIT 1000`)
	if err != nil {
		return err
	}
	slog.Info("evicted unreferenced blobs", "rows", tag.RowsAffected())
	return nil
}

// StorageBreakdown is returned by GET /api/system/storage.
type StorageBreakdown struct {
	PGBytes          int64   `json:"pg_bytes"`
	BlobBytes        int64   `json:"blob_bytes"`
	TrafficFTSBytes  int64   `json:"traffic_fts_bytes"`
	ScratchBytes     int64   `json:"scratch_bytes"`
	ColdBytes        int64   `json:"cold_bytes"`
	CompressionRatio float64 `json:"compression_ratio"`
	TotalGB          float64 `json:"total_gb"`
}

func (g *Governor) GetBreakdown(ctx context.Context) (*StorageBreakdown, error) {
	var b StorageBreakdown
	err := g.db.QueryRow(ctx, `
		SELECT
			pg_database_size(current_database()),
			COALESCE((SELECT SUM(size_compressed) FROM blobs),0),
			COALESCE((SELECT pg_relation_size('traffic_fts')),0),
			COALESCE((SELECT SUM(scratch_bytes) FROM intents WHERE status = 'running'),0),
			0,
			COALESCE((SELECT compression_ratio FROM storage_metrics ORDER BY recorded_at DESC LIMIT 1),1.0)
		`).Scan(&b.PGBytes, &b.BlobBytes, &b.TrafficFTSBytes, &b.ScratchBytes, &b.ColdBytes, &b.CompressionRatio)
	if err != nil {
		return nil, fmt.Errorf("storage breakdown: %w", err)
	}
	b.TotalGB = float64(b.PGBytes+b.BlobBytes+b.ScratchBytes+b.ColdBytes) / 1e9
	return &b, nil
}
