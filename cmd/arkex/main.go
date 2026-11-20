// ArKex 2.0 — single-binary entry point.
// Embeds the Next.js static export and serves it alongside the API.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/threatvector/arkex/agent"
	"github.com/threatvector/arkex/server"
	"github.com/threatvector/arkex/storage"
)

//go:embed web/dist
var webFS embed.FS

var (
	flagAddr   = flag.String("addr", ":8080", "HTTP listen address")
	flagPGDSN  = flag.String("pg", "", "PostgreSQL DSN (overrides ARKEX_DATABASE_URL)")
	flagMigrate = flag.Bool("migrate", false, "Run DB migrations and exit")
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := run(ctx); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	pgDSN := *flagPGDSN
	if pgDSN == "" {
		pgDSN = os.Getenv("ARKEX_DATABASE_URL")
	}
	if pgDSN == "" {
		return fmt.Errorf("ARKEX_DATABASE_URL or -pg required")
	}

	// Connect to PostgreSQL
	pool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		return fmt.Errorf("pg connect: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("pg ping: %w", err)
	}
	slog.Info("postgres connected")

	// Run River migrations
	if err := migrateRiver(ctx, pool); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}

	if *flagMigrate {
		slog.Info("migrations complete, exiting")
		return nil
	}

	// Storage Governor
	scratchDir := os.Getenv("ARKEX_SCRATCH_DIR")
	if scratchDir == "" {
		scratchDir = "/tmp/arkex-scratch"
	}
	gov := storage.NewGovernor(pool, scratchDir)

	// Engine
	engCfg := agent.DefaultConfig()
	eng, err := agent.New(pool, engCfg)
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
	}

	// API Server
	srv := server.New(pool, eng, gov)

	// Serve embedded Next.js static export at /
	distFS, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		slog.Warn("web/dist not embedded, skipping static serving", "err", err)
		distFS = nil
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", srv)
	if distFS != nil {
		mux.Handle("/", http.FileServer(http.FS(distFS)))
	}

	// Start engine
	if err := eng.Start(ctx); err != nil {
		return fmt.Errorf("engine start: %w", err)
	}
	defer func() { _ = eng.Stop(context.Background()) }()

	// Start HTTP server
	httpSrv := &http.Server{
		Addr:    *flagAddr,
		Handler: mux,
	}
	slog.Info("arkex 2.0 ready", "addr", *flagAddr)
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func migrateRiver(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}
