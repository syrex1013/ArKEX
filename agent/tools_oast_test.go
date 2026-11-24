package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

type fakeOAST struct {
	domain    string
	payload   string
	tag       string
	task      string
	intent    string
	listTask  string
	listLimit int
	waitTag   string
	waitTo    time.Duration
	hasTag    string
	has       bool
	noHit     bool
}

func (f *fakeOAST) Domain() string { return f.domain }

func (f *fakeOAST) GeneratePayload(_ context.Context, tag, taskID, intentID string) (string, error) {
	f.tag, f.task, f.intent = tag, taskID, intentID
	f.payload = tag + "-abc123." + f.domain
	return f.payload, nil
}

func (f *fakeOAST) ListRecent(_ context.Context, taskID string, limit int) (any, error) {
	f.listTask, f.listLimit = taskID, limit
	return []map[string]any{{"payload_tag": f.payload, "protocol": "http"}}, nil
}

func (f *fakeOAST) WaitCallback(_ context.Context, tag string, timeout time.Duration) (any, error) {
	f.waitTag, f.waitTo = tag, timeout
	if f.noHit {
		return nil, context.DeadlineExceeded
	}
	return map[string]any{"payload_tag": tag, "protocol": "dns"}, nil
}

func (f *fakeOAST) HasCallback(_ context.Context, tag string) (bool, error) {
	f.hasTag = tag
	return f.has, nil
}

func oastToolsByName(t *testing.T, ts *ToolSet) map[string]actool.CoreTool {
	t.Helper()
	byName := map[string]actool.CoreTool{}
	for _, tl := range ts.WorkerTools() {
		byName[tl.Name()] = tl
	}
	return byName
}

// The OAST tools are worker-visible always and refuse with a clear message when
// the host has not wired a backend (same contract as the lab tools).
func TestOASTToolsInWorkerSetAndRefuseWithoutBackend(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range NewToolSet(nil, "").WorkerTools() {
		names[tl.Name()] = true
	}
	for _, n := range []string{"oast_payload", "list_oast_callbacks", "wait_oast_callback"} {
		if !names[n] {
			t.Fatalf("worker tools missing OAST tool %q", n)
		}
	}
	byName := oastToolsByName(t, NewToolSet(nil, ""))
	for name, input := range map[string]string{
		"oast_payload":        `{"tag":"sqli-oob"}`,
		"list_oast_callbacks": `{}`,
		"wait_oast_callback":  `{"tag":"x.example"}`,
	} {
		res, err := byName[name].Call(t.Context(), json.RawMessage(input), nil)
		if err != nil || !res.IsError {
			t.Fatalf("%s without a backend: err=%v res=%v (want a refusal)", name, err, res)
		}
	}
}

// Wired tools pass the current task id and defaults through to the backend.
func TestOASTToolsDelegateToBackend(t *testing.T) {
	fake := &fakeOAST{domain: "oast.test"}
	ts := NewToolSet(nil, "")
	ts.SetOast(fake)
	ts.SetTaskID(42)
	byName := oastToolsByName(t, ts)

	res, err := byName["oast_payload"].Call(t.Context(), json.RawMessage(`{"tag":"sqli-oob","intent_id":"7"}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("oast_payload: err=%v res=%v", err, res)
	}
	if fake.tag != "sqli-oob" || fake.task != "42" || fake.intent != "7" {
		t.Fatalf("oast_payload args: tag=%q task=%q intent=%q", fake.tag, fake.task, fake.intent)
	}
	if !strings.Contains(res.Flatten(), "sqli-oob-abc123.oast.test") {
		t.Fatalf("oast_payload result missing payload: %q", res.Flatten())
	}

	res, err = byName["list_oast_callbacks"].Call(t.Context(), json.RawMessage(`{"limit":500}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("list_oast_callbacks: err=%v res=%v", err, res)
	}
	if fake.listTask != "42" || fake.listLimit != 200 {
		t.Fatalf("list_oast_callbacks args: task=%q limit=%d (want clamped 200)", fake.listTask, fake.listLimit)
	}

	res, err = byName["wait_oast_callback"].Call(t.Context(), json.RawMessage(`{"tag":"x-abc123.oast.test","timeout_seconds":999}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("wait_oast_callback hit: err=%v res=%v", err, res)
	}
	if fake.waitTag != "x-abc123.oast.test" || fake.waitTo != maxOASTWait {
		t.Fatalf("wait_oast_callback args: tag=%q timeout=%s (want clamped %s)", fake.waitTag, fake.waitTo, maxOASTWait)
	}

	// A timeout is a real result, not a tool error: absence of a hit matters.
	fake.noHit = true
	res, err = byName["wait_oast_callback"].Call(t.Context(), json.RawMessage(`{"tag":"x-abc123.oast.test","timeout_seconds":0}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("wait_oast_callback timeout: err=%v res=%v (want a plain result)", err, res)
	}
	if !strings.Contains(res.Flatten(), "no OAST callback") {
		t.Fatalf("timeout result should state the absence: %q", res.Flatten())
	}
}

// timeoutTool must bound a hung handler: past the deadline the call returns a
// tool error-result (never a transport error, never hangs forever).
func TestTimeoutToolBoundsHungCall(t *testing.T) {
	old := toolTimeout
	toolTimeout = 50 * time.Millisecond
	t.Cleanup(func() { toolTimeout = old })

	slow := actool.Build(actool.Spec{
		Name: "slow_tool",
		Run: func(ctx context.Context, _ json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			<-ctx.Done()
			return actool.Text("unreachable"), ctx.Err()
		},
	})
	start := time.Now()
	res, err := timeoutTool(slow).Call(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Flatten(), "timed out after 50ms") {
		t.Fatalf("want a timeout tool error, got IsError=%v %q", res.IsError, res.Flatten())
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout tool hung for %s", elapsed)
	}

	fast := actool.Build(actool.Spec{
		Name: "fast_tool",
		Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
			return actool.Text("ok"), nil
		},
	})
	res, err = timeoutTool(fast).Call(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil || res.IsError || res.Flatten() != "ok" {
		t.Fatalf("fast tool passthrough broken: err=%v res=%v", err, res)
	}
}
