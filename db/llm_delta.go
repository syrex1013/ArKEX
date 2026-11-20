package db

import (
	"os"
	"strings"
)

// LLMMessage is one append-only delta turn for a session.
type LLMMessage struct {
	SessionID string
	TurnSeq   int
	Role      string
	Content   []byte // plaintext; stored zstd-compressed
	SHA256    string
}

// InsertLLMMessage appends one delta message. Conflicts on (session_id, turn_seq) are ignored.
func (d *DB) InsertLLMMessage(m *LLMMessage) error {
	_, err := d.Exec(`
INSERT INTO llm_messages(session_id, turn_seq, role, content_zstd, sha256)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (session_id, turn_seq) DO NOTHING`,
		m.SessionID, m.TurnSeq, m.Role, m.Content, m.SHA256)
	return err
}

// NextLLMMessageSeq returns the next turn_seq for session (max+1, or 0).
func (d *DB) NextLLMMessageSeq(sessionID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COALESCE(MAX(turn_seq)+1, 0) FROM llm_messages WHERE session_id=$1`, sessionID).Scan(&n)
	return n, err
}

// InsertLLMRawDebug stores opt-in raw wire bodies into the 24h ring buffer.
func (d *DB) InsertLLMRawDebug(sessionID, taskID, rawReq, rawResp string) error {
	// llm_raw_debug.raw_* are BYTEA: bind as []byte so the wire bytes go in as-is.
	_, err := d.Exec(`
INSERT INTO llm_raw_debug(session_id, task_id, raw_request, raw_response)
VALUES ($1,$2,$3,$4)`,
		nullIfEmpty(sessionID), nullIfEmpty(taskID), []byte(rawReq), []byte(rawResp))
	return err
}

// LLMRawDebugEnabled reports whether permanent-looking raw wire capture is opted in.
// Default: off. Set ARKEX_LLM_RAW_DEBUG=1 (or ARTEX_LLM_RAW_DEBUG=1) to enable.
func LLMRawDebugEnabled() bool {
	for _, k := range []string{"ARKEX_LLM_RAW_DEBUG", "ARTEX_LLM_RAW_DEBUG"} {
		v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
		if v == "1" || v == "true" || v == "on" || v == "yes" {
			return true
		}
	}
	return false
}

// InsertLLMRecordDelta stores a compact llm_records row (no full history replay,
// no raw wire unless caller fills Raw*).
func (d *DB) InsertLLMRecordDelta(r *LLMRecord, upToSeq int, requestSHA string) error {
	_, err := d.Exec(`
INSERT INTO llm_records(model, profile_name, session_id, task_id, worker, latency_ms,
	input_tokens, output_tokens, cache_read, cache_write, status, error,
	request_body, response_body, raw_request, raw_response, up_to_seq, request_sha256)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		r.Model, nullIfEmpty(r.ProfileName), r.SessionID, nullIfEmpty(r.TaskID), nullIfEmpty(r.Worker),
		r.LatencyMs, r.InputTokens, r.OutputTokens, r.CacheRead, r.CacheWrite,
		r.Status, nullIfEmpty(r.Error), nullIfEmpty(r.RequestBody), nullIfEmpty(r.ResponseBody),
		nullIfEmpty(r.RawRequest), nullIfEmpty(r.RawResponse), upToSeq, nullIfEmpty(requestSHA))
	return err
}
