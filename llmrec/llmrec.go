// Package llmrec implements delta-encoded LLM conversation recording.
// Never stores O(N²) full histories — only appends new turns.
package llmrec

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threatvector/arkex/storage"
)

// Recorder appends LLM turns to llm_messages without duplicating prior turns.
type Recorder struct {
	db    *pgxpool.Pool
	codec *storage.Codec
}

func New(db *pgxpool.Pool) *Recorder {
	return &Recorder{db: db, codec: storage.DefaultCodec()}
}

// Message is a single LLM turn to record.
type Message struct {
	Role    string // "system" | "user" | "assistant" | "tool"
	Content []byte // raw (uncompressed) content
}

// AppendTurn appends one new turn to the session's message log.
// It only writes the NEW turn — it never re-serializes prior turns.
func (r *Recorder) AppendTurn(ctx context.Context, sessionID string, turnSeq int, msg *Message) error {
	compressed, sha, err := r.codec.Compress(msg.Content)
	if err != nil {
		return fmt.Errorf("compress: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO llm_messages (session_id, turn_seq, role, content_zstd, sha256, token_count)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (session_id, turn_seq) DO NOTHING`,
		sessionID, turnSeq, msg.Role, compressed, sha, estimateTokens(msg.Content),
	)
	if err != nil {
		return fmt.Errorf("insert llm_message: %w", err)
	}
	return nil
}

// RecordCall stores metadata for one LLM API call (not the content — only refs).
func (r *Recorder) RecordCall(ctx context.Context, c *CallRecord) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO llm_calls
			(session_id, intent_id, up_to_seq, response_seq, model, profile_id,
			 tokens_in, tokens_out, cache_read, latency_ms, status, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		c.SessionID, c.IntentID, c.UpToSeq, c.ResponseSeq, c.Model, c.ProfileID,
		c.TokensIn, c.TokensOut, c.CacheRead, c.LatencyMS, c.Status, c.Error,
	)
	return err
}

// ReadSession reconstructs a session's messages up to and including maxSeq.
func (r *Recorder) ReadSession(ctx context.Context, sessionID string, maxSeq int) ([]*Message, error) {
	rows, err := r.db.Query(ctx, `
		SELECT role, content_zstd
		FROM llm_messages
		WHERE session_id = $1 AND turn_seq <= $2
		ORDER BY turn_seq ASC`,
		sessionID, maxSeq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		var role string
		var compressed []byte
		if err := rows.Scan(&role, &compressed); err != nil {
			return nil, err
		}
		content, err := r.codec.Decompress(compressed)
		if err != nil {
			return nil, fmt.Errorf("decompress turn: %w", err)
		}
		msgs = append(msgs, &Message{Role: role, Content: content})
	}
	return msgs, rows.Err()
}

// NextSeq returns the next turn_seq for sessionID (0 if no turns yet).
func (r *Recorder) NextSeq(ctx context.Context, sessionID string) (int, error) {
	var seq int
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(turn_seq)+1, 0) FROM llm_messages WHERE session_id = $1`,
		sessionID,
	).Scan(&seq)
	return seq, err
}

// SessionStats returns token counts for a session.
func (r *Recorder) SessionStats(ctx context.Context, sessionID string) (*SessionStats, error) {
	var s SessionStats
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) AS turns,
			COALESCE(SUM(token_count), 0) AS total_tokens,
			COALESCE(SUM(octet_length(content_zstd)), 0) AS compressed_bytes,
			COALESCE(SUM(token_count)*4, 0) AS estimated_raw_bytes
		FROM llm_messages WHERE session_id = $1`,
		sessionID,
	).Scan(&s.Turns, &s.TotalTokens, &s.CompressedBytes, &s.EstimatedRawBytes)
	if err != nil {
		return nil, err
	}
	if s.EstimatedRawBytes > 0 {
		s.CompressionRatio = float64(s.EstimatedRawBytes) / float64(s.CompressedBytes)
	}
	return &s, nil
}

// PurgeOldRawDebug deletes raw debug captures older than 24h (called by Governor).
func (r *Recorder) PurgeOldRawDebug(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM llm_raw_debug WHERE created_at < $1`,
		time.Now().Add(-24*time.Hour),
	)
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if n > 0 {
		slog.Info("purged llm_raw_debug", "rows", n)
	}
	return n, nil
}

type CallRecord struct {
	SessionID   string
	IntentID    *string
	UpToSeq     int
	ResponseSeq int
	Model       string
	ProfileID   *string
	TokensIn    int
	TokensOut   int
	CacheRead   int
	LatencyMS   int
	Status      string
	Error       *string
}

type SessionStats struct {
	Turns             int
	TotalTokens       int64
	CompressedBytes   int64
	EstimatedRawBytes int64
	CompressionRatio  float64
}

// estimateTokens approximates token count as len/4 (rough BPE estimate).
func estimateTokens(content []byte) int {
	return len(content) / 4
}
