package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// CAS is a content-addressed blob store in PostgreSQL (Zstd-compressed bodies).
type CAS struct {
	db    *sql.DB
	codec *Codec
}

func NewCAS(db *sql.DB, codec *Codec) *CAS {
	if codec == nil {
		codec = DefaultCodec()
	}
	return &CAS{db: db, codec: codec}
}

// PutBody stores body once by SHA-256 and returns the hash.
func (c *CAS) PutBody(ctx context.Context, body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}
	compressed, sha, err := c.codec.Compress(body)
	if err != nil {
		return "", fmt.Errorf("cas compress: %w", err)
	}
	_, err = c.db.ExecContext(ctx, `
INSERT INTO blobs (sha256, size_original, size_compressed, data_zstd)
VALUES ($1, $2, $3, $4)
ON CONFLICT (sha256) DO NOTHING`,
		sha, len(body), len(compressed), compressed)
	if err != nil {
		return "", fmt.Errorf("cas insert: %w", err)
	}
	return sha, nil
}

// GetBody loads and decompresses a blob by SHA-256.
func (c *CAS) GetBody(ctx context.Context, sha256 string) ([]byte, error) {
	if sha256 == "" {
		return nil, nil
	}
	var compressed []byte
	err := c.db.QueryRowContext(ctx, `SELECT data_zstd FROM blobs WHERE sha256 = $1`, sha256).Scan(&compressed)
	if err != nil {
		return nil, fmt.Errorf("cas get: %w", err)
	}
	return c.codec.Decompress(compressed)
}
