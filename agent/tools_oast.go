package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

// OASTBackend exposes the host's out-of-band callback correlator (offensive/oast)
// to agents: mint callback payloads, list received interactions, and block on a
// hit. The server wires an implementation via ToolSet.SetOast / Worker.SetOast;
// nil keeps the tools reporting "currently unavailable" (same contract as SetLab).
type OASTBackend interface {
	// Domain returns the OAST callback domain the payloads point at.
	Domain() string
	// GeneratePayload registers a unique callback host for tag/taskID and
	// returns the full host payload to embed in requests.
	GeneratePayload(ctx context.Context, tag, taskID, intentID string) (string, error)
	// ListRecent returns recent callbacks for taskID (empty = all).
	ListRecent(ctx context.Context, taskID string, limit int) (any, error)
	// WaitCallback blocks until a hit for the payload tag arrives or the
	// timeout ends. Returns the callback record, or an error on timeout.
	WaitCallback(ctx context.Context, tag string, timeout time.Duration) (any, error)
	// HasCallback reports whether the payload tag has a received interaction.
	HasCallback(ctx context.Context, tag string) (bool, error)
}

// SetOast wires the OAST backend for this ToolSet. nil (the zero value) leaves
// the tools callable but refusing with a clear message.
func (t *ToolSet) SetOast(o OASTBackend) { t.oast = o }

// maxOASTWait bounds wait_oast_callback so it always finishes inside the
// per-tool timeout backstop (300s).
const maxOASTWait = 120 * time.Second

// oastPayloadTool mints a unique OAST callback host for the task.
func (t *ToolSet) oastPayloadTool() actool.CoreTool {
	return writeTool("oast_payload",
		"Mint a unique out-of-band (OAST) callback hostname for this task and return it. Embed the returned host in crafted requests (Host header, SSRF URL, XXE endpoint, DNS name, …) tagged with your own label; when the target interacts with it, the callback is correlated to your label and the planner is woken. Check hits with list_oast_callbacks or block on one with wait_oast_callback. Use the full returned payload string when waiting/checking.",
		obj(map[string]any{
			"tag":       str("short label correlating this payload to your hypothesis, e.g. sqli-oob or xxe-stage1"),
			"intent_id": str("optional: intent id this payload belongs to (default: current intent context)"),
		}, "tag"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.oast == nil {
				return actool.Errorf("oast_payload is currently unavailable"), nil
			}
			var a struct {
				Tag      string `json:"tag"`
				IntentID string `json:"intent_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.Tag) == "" {
				return actool.Errorf("tag is required"), nil
			}
			payload, err := t.oast.GeneratePayload(ctx, strings.TrimSpace(a.Tag), strconv.FormatInt(t.taskID, 10), a.IntentID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return labJSON(map[string]any{"payload": payload, "domain": t.oast.Domain(), "tag": strings.TrimSpace(a.Tag)})
		})
}

// listOASTCallbacksTool lists recent OAST callbacks for this task.
func (t *ToolSet) listOASTCallbacksTool() actool.CoreTool {
	return readTool("list_oast_callbacks",
		"List the out-of-band (OAST) callbacks received for this task, newest first (payload tag, protocol, source, data, time). Cross-check the payload tags you minted with oast_payload to confirm out-of-band interaction (blind SSRF/XXE/SQLi/RCE receipts).",
		obj(map[string]any{
			"limit": intp("return cap, default 50 (optional)"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.oast == nil {
				return actool.Errorf("list_oast_callbacks is currently unavailable"), nil
			}
			var a struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 {
				a.Limit = 50
			}
			if a.Limit > 200 { // context-blowup guard, same rationale as list_assets
				a.Limit = 200
			}
			res, err := t.oast.ListRecent(ctx, strconv.FormatInt(t.taskID, 10), a.Limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return labJSON(res)
		})
}

// waitOASTCallbackTool blocks until a callback for the given payload tag arrives.
func (t *ToolSet) waitOASTCallbackTool() actool.CoreTool {
	return readTool("wait_oast_callback",
		"Block until an out-of-band (OAST) callback for the given payload (from oast_payload) arrives, or until the timeout ends. Use after injecting a callback payload into a target whose response channel is blind. Returns the callback record, or a timeout notice (absence of a hit is a real result, not an error to retry).",
		obj(map[string]any{
			"tag":             str("the full payload host returned by oast_payload"),
			"timeout_seconds": intp("how long to wait, 1-120 (default 60)"),
		}, "tag"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.oast == nil {
				return actool.Errorf("wait_oast_callback is currently unavailable"), nil
			}
			var a struct {
				Tag     string `json:"tag"`
				Timeout int    `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.Tag) == "" {
				return actool.Errorf("tag is required"), nil
			}
			wait := 60 * time.Second
			if a.Timeout > 0 {
				wait = time.Duration(a.Timeout) * time.Second
			}
			if wait > maxOASTWait {
				wait = maxOASTWait
			}
			res, err := t.oast.WaitCallback(ctx, strings.TrimSpace(a.Tag), wait)
			if err != nil {
				if ctx.Err() != nil {
					return actool.Errorf("wait_oast_callback cancelled"), nil
				}
				return actool.Text(fmt.Sprintf("no OAST callback within %s for %s (absence of a hit is itself a result — do not treat this as a tool failure)", wait, strings.TrimSpace(a.Tag))), nil
			}
			return labJSON(res)
		})
}
