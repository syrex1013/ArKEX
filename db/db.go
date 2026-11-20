// Package db handles PostgreSQL connection setup and schema migration for ArKex 2.0.
package db

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaDDL string

// Connect creates a pgx connection pool from the given DSN.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg config: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return pool, nil
}

// Migrate applies schema.sql to the database (idempotent — uses IF NOT EXISTS).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	slog.Info("running schema migration")
	if _, err := pool.Exec(ctx, schemaDDL); err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}
	slog.Info("schema migration complete")
	return nil
}
