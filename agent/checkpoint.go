package agent

// Checkpointing: the durable progress record of one worker run (docs
// long-horizon swarm). The checkpointer observes every tool call, keeps a
// bounded summary of done/open items and written artifacts, and persists it as
// an intent_checkpoint on an interval — so a resumed run continues from
// evidence instead of repeating finished work.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// CheckpointStore is the write side of the durable progress record.
// *db.ExplorationStore's underlying DB satisfies it.
type CheckpointStore interface {
	AddCheckpoint(db.IntentCheckpoint) (int64, error)
}

const (
	// checkpointInterval is how often a non-forced flush may write; bursts of
	// tool calls between two flushes collapse into one row.
	checkpointInterval = 5 * time.Second
	// checkpointArtifactCap bounds the artifacts list (deduplicated).
	checkpointArtifactCap = 40
	// checkpointSummaryCap trims every done/open summary.
	checkpointSummaryCap = 200
)

// Checkpointer accumulates per-tool-call progress of one worker run.
type Checkpointer struct {
	mu       sync.Mutex
	store    CheckpointStore
	taskID   int64
	intentID int64
	agent    string
	attempt  int
	interval time.Duration
	calls    int
	last     time.Time
	// wroteCalls is the call count captured at the last successful write;
	// non-forced flushes skip while nothing new was observed.
	wroteCalls            int
	phase                 string
	done, open, artifacts []string
	// onWrite, set by the worker (same package), records one checkpoint agent
	// event per successful write.
	onWrite func()
}

// NewCheckpointer returns a checkpointer writing to store. attempt defaults
// to 1 when <1.
func NewCheckpointer(store CheckpointStore, taskID, intentID int64, agent string, attempt int) *Checkpointer {
	if attempt < 1 {
		attempt = 1
	}
	return &Checkpointer{store: store, taskID: taskID, intentID: intentID, agent: agent, attempt: attempt, interval: checkpointInterval}
}

// Observe records one tool call: the counter and the phase (the tool name),
// plus a trimmed open summary for non-write tools. Write/Edit calls are not
// recorded here — a call observed before execution is not completed work; the
// result decides. See CompleteWrite.
func (c *Checkpointer) Observe(tool, input string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.phase = tool
	switch tool {
	case "Write", "Edit":
		// Completion is recorded from the tool result, not the call.
	default:
		c.open = append(c.open, checkpointSummary(input))
	}
}

// CompleteWrite records one Write/Edit tool result. Only a non-error result
// counts as completed work: the artifact path (file_path/target_path, dedup,
// cap 40) and the done summary. A failed write claims neither — it must not
// surface in a resumed run as finished.
func (c *Checkpointer) CompleteWrite(tool, input string, isErr bool) {
	if isErr || (tool != "Write" && tool != "Edit") {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if path := checkpointArtifactPath(input); path != "" {
		seen := false
		for _, a := range c.artifacts {
			if a == path {
				seen = true
				break
			}
		}
		if !seen {
			if len(c.artifacts) < checkpointArtifactCap {
				c.artifacts = append(c.artifacts, path)
			}
		}
	}
	c.done = append(c.done, checkpointSummary(input))
}

// Finish persists a checkpoint unless a non-forced flush is redundant: nothing
// new observed since the last write, or the flush interval has not elapsed.
// force always writes. next becomes the stored next step. Write failures are
// logged, never returned — a checkpoint is an optimization, not a gate.
func (c *Checkpointer) Finish(next string, force bool) {
	c.mu.Lock()
	if !force && (c.calls == c.wroteCalls || time.Since(c.last) < c.interval) {
		c.mu.Unlock()
		return
	}
	cp := db.IntentCheckpoint{
		TaskID:    c.taskID,
		IntentID:  c.intentID,
		AgentName: c.agent,
		Attempt:   c.attempt,
		Phase:     c.phase,
		NextStep:  next,
		Done:      append([]string(nil), c.done...),
		Open:      append([]string(nil), c.open...),
		Artifacts: append([]string(nil), c.artifacts...),
		ToolCalls: c.calls,
	}
	c.wroteCalls = c.calls
	c.last = time.Now()
	onWrite := c.onWrite
	c.mu.Unlock()
	if _, err := c.store.AddCheckpoint(cp); err != nil {
		log.Printf("[agent-lifecycle] task=%d agent=%s intent=%d kind=%s checkpoint FAILED: %v",
			c.taskID, c.agent, c.intentID, db.EventCheckpoint, err)
		return
	}
	if onWrite != nil {
		onWrite()
	}
}

// checkpointSummary trims the most informative input field to 200 chars.
func checkpointSummary(input string) string {
	var p map[string]any
	if json.Unmarshal([]byte(input), &p) == nil {
		for _, key := range []string{"description", "command", "prompt", "content"} {
			if s, ok := p[key].(string); ok && s != "" {
				return trimRunes(s, checkpointSummaryCap)
			}
		}
	}
	return trimRunes(strings.TrimSpace(input), checkpointSummaryCap)
}

// checkpointArtifactPath pulls file_path/target_path out of a Write/Edit input.
func checkpointArtifactPath(input string) string {
	var p map[string]any
	if json.Unmarshal([]byte(input), &p) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "target_path"} {
		if s, ok := p[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// trimRunes cuts s to at most max runes so a summary never splits a rune.
func trimRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// checkpointHooks wraps the per-run hooks with the checkpointer: every tool
// call is observed and every result triggers a (rate-limited) flush.
type checkpointHooks struct {
	inner harness.HookRunner
	cp    *Checkpointer
}

// WithCheckpoint returns the hooks of a run whose progress c records.
// inner may be nil.
func WithCheckpoint(inner harness.HookRunner, c *Checkpointer) harness.HookRunner {
	if c == nil {
		return inner
	}
	return checkpointHooks{inner: inner, cp: c}
}

func (h checkpointHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	h.cp.Observe(name, string(input))
	if h.inner != nil {
		return h.inner.PreToolUse(ctx, name, input)
	}
	return false, "", nil
}

func (h checkpointHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	h.cp.CompleteWrite(name, string(input), isErr)
	h.cp.Finish("", false)
	if h.inner != nil {
		h.inner.PostToolUse(ctx, name, input, result, isErr)
	}
}

func (h checkpointHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	if h.inner != nil {
		return h.inner.Stop(ctx, messages)
	}
	return false, nil, ""
}

// lastAssistantText returns the text of the newest assistant message; empty
// when there is none. It becomes the checkpoint's next step.
func lastAssistantText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant {
			return messages[i].Text()
		}
	}
	return ""
}
