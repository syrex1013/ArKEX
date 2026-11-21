package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

// LabTechniques exposes the host's offensive lab techniques (HTTP desync
// probing, CPG/taint sink indexing, hybrid fuzzing) to agents. The server wires
// an implementation via ToolSet.SetLab / Worker.SetLab; nil keeps the tools
// reporting "currently unavailable" (same contract as kill_work).
type LabTechniques interface {
	// DesyncProbe runs the desync/smuggling probe suite against url and records
	// the run under taskID. Returns the probe results.
	DesyncProbe(ctx context.Context, url, taskID string) (any, error)
	// CPGIndex indexes the source tree at path (must live under the server data
	// directory) for taint sinks. Returns {sinks, count}.
	CPGIndex(ctx context.Context, taskID, path, language string) (any, error)
	// CPGSinks lists the taint sinks previously indexed for taskID.
	CPGSinks(ctx context.Context, taskID string) (any, error)
	// LaunchFuzz queues a fuzz job and returns its job id.
	LaunchFuzz(ctx context.Context, taskID, kind, target, targetKind string) (int64, error)
}

// SetLab wires the lab technique backend for this ToolSet. nil (the zero value)
// leaves the tools callable but refusing with a clear message.
func (t *ToolSet) SetLab(l LabTechniques) { t.lab = l }

// labJSON marshals a lab result into a tool text result.
func labJSON(v any) (actool.Result, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(fmt.Sprintf("lab result encoding failed: %v", err)), nil
	}
	return actool.Text(string(raw)), nil
}

// desyncProbeTool runs HTTP request smuggling / desync probes against a URL.
func (t *ToolSet) desyncProbeTool() actool.CoreTool {
	return writeTool("desync_probe",
		"Run HTTP request-smuggling/desync probes (CL.TE, TE.CL, H2.CL, H2.TE, pipelined race) against a target URL and return per-technique results. Use when the target's HTTP stack looks smugglable (proxy/CDN chains, mixed HTTP versions) or desync is a candidate for cache poisoning. Read-only probes; never destructive.",
		obj(map[string]any{"url": str("absolute http(s) URL of the target endpoint to probe")}, "url"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.lab == nil {
				return actool.Errorf("desync_probe is currently unavailable"), nil
			}
			var a struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.URL) == "" {
				return actool.Errorf("url is required"), nil
			}
			res, err := t.lab.DesyncProbe(ctx, a.URL, strconv.FormatInt(t.taskID, 10))
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return labJSON(res)
		})
}

// cpgIndexTool indexes a source tree under the server data dir for taint sinks.
func (t *ToolSet) cpgIndexTool() actool.CoreTool {
	return writeTool("index_cpg_sinks",
		"Index a local source tree (harvested source, repo checkout) with the CPG/taint analyzer and return the detected dangerous sink list {sinks, count}. path must stay under the server data directory. Run list_cpg_sinks afterwards or re-index after new source is harvested.",
		obj(map[string]any{
			"path":     str("path of the source tree to index (absolute under the data dir, or relative to it)"),
			"language": str("optional: language hint (auto default)"),
		}, "path"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.lab == nil {
				return actool.Errorf("index_cpg_sinks is currently unavailable"), nil
			}
			var a struct {
				Path     string `json:"path"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.Path) == "" {
				return actool.Errorf("path is required"), nil
			}
			if a.Language == "" {
				a.Language = "auto"
			}
			res, err := t.lab.CPGIndex(ctx, strconv.FormatInt(t.taskID, 10), a.Path, a.Language)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return labJSON(res)
		})
}

// cpgSinksTool lists previously indexed taint sinks for this task.
func (t *ToolSet) cpgSinksTool() actool.CoreTool {
	return readTool("list_cpg_sinks",
		"List the taint sinks found by previous index_cpg_sinks runs for this task (file, line, sink kind, confidence). Use to pick exploitation targets inside harvested source.",
		obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			if t.lab == nil {
				return actool.Errorf("list_cpg_sinks is currently unavailable"), nil
			}
			res, err := t.lab.CPGSinks(ctx, strconv.FormatInt(t.taskID, 10))
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return labJSON(res)
		})
}

// fuzzTool queues a hybrid fuzz job.
func (t *ToolSet) fuzzTool() actool.CoreTool {
	return writeTool("launch_fuzz",
		"Queue a hybrid fuzz job (afl++ default) against a target and return {job_id}. Target is a binary/service endpoint under the data dir or an in-scope URL-shaped target; check the job's output through the filesystem afterwards. Use for deep input-parsing coverage when enumeration suggests a parser worth fuzzing.",
		obj(map[string]any{
			"target":      str("fuzz target: executable path under the data dir or endpoint descriptor"),
			"kind":        str("optional: fuzzer kind (afl++ default)"),
			"target_kind": str("optional: target kind hint"),
		}, "target"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.lab == nil {
				return actool.Errorf("launch_fuzz is currently unavailable"), nil
			}
			var a struct {
				Target     string `json:"target"`
				Kind       string `json:"kind"`
				TargetKind string `json:"target_kind"`
			}
			if err := json.Unmarshal(in, &a); err != nil || strings.TrimSpace(a.Target) == "" {
				return actool.Errorf("target is required"), nil
			}
			if a.Kind == "" {
				a.Kind = "afl++"
			}
			id, err := t.lab.LaunchFuzz(ctx, strconv.FormatInt(t.taskID, 10), a.Kind, a.Target, a.TargetKind)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("fuzz job %d queued (kind %s)", id, a.Kind)), nil
		})
}
