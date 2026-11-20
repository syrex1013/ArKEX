package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMaxDiskGB      = 20
	DefaultScratchQuotaMB = 256
	FTSRetentionDays      = 7
	LLMRawDebugTTLHours   = 24
)

// Governor enforces disk quotas and TTL cleanup against an ARTEX data directory
// and PostgreSQL. It does not delete finding-linked evidence.
type Governor struct {
	db        *sql.DB
	dataDir   string
	maxDiskGB int64
	scratchMB int64
}

func NewGovernor(db *sql.DB, dataDir string) *Governor {
	maxGB := int64(DefaultMaxDiskGB)
	if v := strings.TrimSpace(os.Getenv("ARKEX_MAX_DISK_GB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxGB = n
		}
	}
	scratch := int64(DefaultScratchQuotaMB)
	if v := strings.TrimSpace(os.Getenv("ARKEX_SCRATCH_QUOTA_MB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			scratch = n
		}
	}
	return &Governor{db: db, dataDir: dataDir, maxDiskGB: maxGB, scratchMB: scratch}
}

// ScratchQuotaBytes is the per-intent scratch cap.
func (g *Governor) ScratchQuotaBytes() int64 { return g.scratchMB << 20 }

// RunOnce executes one governor pass.
func (g *Governor) RunOnce(ctx context.Context) error {
	start := time.Now()
	var first error
	capture := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	capture(g.purgeLLMRawDebug(ctx))
	capture(g.cleanClosedTaskScratch(ctx))
	capture(g.recordMetrics(ctx))
	slog.Info("storage governor run", "elapsed", time.Since(start), "err", first)
	return first
}

// StartPeriodic runs RunOnce on interval until ctx is done.
func (g *Governor) StartPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		_ = g.RunOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = g.RunOnce(ctx)
			}
		}
	}()
}

func (g *Governor) purgeLLMRawDebug(ctx context.Context) error {
	if g.db == nil {
		return nil
	}
	res, err := g.db.ExecContext(ctx,
		`DELETE FROM llm_raw_debug WHERE created_at < now() - make_interval(hours => $1)`, LLMRawDebugTTLHours)
	if err != nil {
		// Table may not exist yet on pre-migration DBs during tests.
		if strings.Contains(err.Error(), "llm_raw_debug") {
			return nil
		}
		return err
	}
	n, _ := res.RowsAffected()
	slog.Debug("purged llm_raw_debug", "rows", n)
	return nil
}

// cleanClosedTaskScratch removes orphan cmd-output under closed tasks older than 1h,
// keeping anything whose name suggests finding/evidence linkage.
func (g *Governor) cleanClosedTaskScratch(ctx context.Context) error {
	if g.db == nil || g.dataDir == "" {
		return nil
	}
	rows, err := g.db.QueryContext(ctx, `
		SELECT id::text FROM tasks
		WHERE status IN ('done','failed','timeout')
		  AND deleted_at IS NULL
		  AND COALESCE(completed_at, updated_at, created_at) < now() - interval '1 hour'
		LIMIT 200`)
	if err != nil {
		// tasks schema variants — best-effort
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		dir := filepath.Join(g.dataDir, "tasks", id)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			base := strings.ToLower(info.Name())
			if strings.Contains(base, "finding") || strings.Contains(base, "evidence") || strings.Contains(base, "loot") {
				return nil
			}
			if strings.Contains(path, "cmd-output") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".log") {
				_ = os.Remove(path)
			}
			return nil
		})
	}
	return nil
}

// Breakdown is the GET /api/system/storage payload.
type Breakdown struct {
	PGBytes          int64   `json:"pg_bytes"`
	TrafficBytes     int64   `json:"traffic_bytes"`
	ScratchBytes     int64   `json:"scratch_bytes"`
	BlobBytes        int64   `json:"blob_bytes"`
	TotalGB          float64 `json:"total_gb"`
	MaxDiskGB        int64   `json:"max_disk_gb"`
	ScratchQuotaMB   int64   `json:"scratch_quota_mb"`
	CompressionRatio float64 `json:"compression_ratio"`
	SavedBytes       int64   `json:"saved_bytes"`
}

func (g *Governor) Breakdown(ctx context.Context) (Breakdown, error) {
	var b Breakdown
	b.MaxDiskGB = g.maxDiskGB
	b.ScratchQuotaMB = g.scratchMB
	if g.dataDir != "" {
		b.TrafficBytes = dirSize(filepath.Join(g.dataDir, "traffic"))
		b.ScratchBytes = dirSize(filepath.Join(g.dataDir, "tasks"))
		b.BlobBytes = dirSize(filepath.Join(g.dataDir, "traffic", "_blobs"))
	}
	if g.db != nil {
		_ = g.db.QueryRowContext(ctx, `
			SELECT COALESCE(pg_database_size(current_database()), 0)`).Scan(&b.PGBytes)
		var orig, comp sql.NullInt64
		_ = g.db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(size_original),0), COALESCE(SUM(size_compressed),0)
			FROM storage_metrics WHERE kind = 'blob'`).Scan(&orig, &comp)
		if orig.Valid && orig.Int64 > 0 && comp.Valid {
			b.CompressionRatio = float64(comp.Int64) / float64(orig.Int64)
			b.SavedBytes = orig.Int64 - comp.Int64
		}
	}
	total := b.PGBytes + b.TrafficBytes + b.ScratchBytes
	b.TotalGB = float64(total) / (1 << 30)
	return b, nil
}

func (g *Governor) recordMetrics(ctx context.Context) error {
	if g.db == nil {
		return nil
	}
	b, err := g.Breakdown(ctx)
	if err != nil {
		return err
	}
	_, err = g.db.ExecContext(ctx, `
		INSERT INTO storage_metrics(kind, size_original, size_compressed, meta)
		VALUES ('snapshot', $1, $2, jsonb_build_object(
			'traffic_bytes', $3::bigint,
			'scratch_bytes', $4::bigint,
			'blob_bytes', $5::bigint,
			'total_gb', $6::float8
		))`,
		b.PGBytes+b.TrafficBytes+b.ScratchBytes,
		b.PGBytes+b.BlobBytes+b.ScratchBytes,
		b.TrafficBytes, b.ScratchBytes, b.BlobBytes, b.TotalGB,
	)
	if err != nil && strings.Contains(err.Error(), "storage_metrics") {
		return nil
	}
	return err
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// EnforceScratchQuota returns an error when dir already exceeds the per-intent cap.
func (g *Governor) EnforceScratchQuota(intentDir string) error {
	n := dirSize(intentDir)
	cap := g.ScratchQuotaBytes()
	if n > cap {
		return fmt.Errorf("scratch quota exceeded: %d bytes > %d bytes cap", n, cap)
	}
	return nil
}
