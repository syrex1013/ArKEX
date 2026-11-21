package agent

import (
	"context"
	"encoding/json"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

type fakeLab struct {
	desyncURL   string
	desyncTask  string
	cpgTask     string
	cpgPath     string
	cpgLanguage string
	fuzzTask    string
	fuzzKind    string
	fuzzTarget  string
}

func (f *fakeLab) DesyncProbe(_ context.Context, url, taskID string) (any, error) {
	f.desyncURL, f.desyncTask = url, taskID
	return []map[string]any{{"technique": "cl.te", "smuggled": false}}, nil
}

func (f *fakeLab) CPGIndex(_ context.Context, taskID, path, language string) (any, error) {
	f.cpgTask, f.cpgPath, f.cpgLanguage = taskID, path, language
	return map[string]any{"sinks": []string{"exec"}, "count": 1}, nil
}

func (f *fakeLab) CPGSinks(_ context.Context, taskID string) (any, error) {
	return map[string]any{"sinks": []string{"exec"}, "task": taskID}, nil
}

func (f *fakeLab) LaunchFuzz(_ context.Context, taskID, kind, target, _ string) (int64, error) {
	f.fuzzTask, f.fuzzKind, f.fuzzTarget = taskID, kind, target
	return 7, nil
}

// TestLabToolsInWorkerSet: the four lab techniques are part of the worker set
// (kill_work contract: present always, refusing at call time when the host has
// not wired a backend).
func TestLabToolsInWorkerSet(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range NewToolSet(nil, "").WorkerTools() {
		names[tl.Name()] = true
	}
	for _, n := range []string{"desync_probe", "index_cpg_sinks", "list_cpg_sinks", "launch_fuzz"} {
		if !names[n] {
			t.Fatalf("worker tools missing lab tool %q", n)
		}
	}
}

// TestLabToolsUnavailableWithoutBackend: with no lab wired the tools refuse
// with a clear message instead of panicking.
func TestLabToolsUnavailableWithoutBackend(t *testing.T) {
	ts := NewToolSet(nil, "")
	byName := map[string]actool.CoreTool{}
	for _, tl := range ts.WorkerTools() {
		byName[tl.Name()] = tl
	}
	inputs := map[string]string{
		"desync_probe":    `{"url":"http://target.example"}`,
		"index_cpg_sinks": `{"path":"lab/src"}`,
		"launch_fuzz":     `{"target":"lab/src/server"}`,
	}
	for name, input := range inputs {
		res, err := byName[name].Call(t.Context(), json.RawMessage(input), nil)
		if err != nil || !res.IsError {
			t.Fatalf("%s without a backend: err=%v res=%v (want a refusal)", name, err, res)
		}
	}
}

// TestLabToolsDelegateToBackend: wired tools pass the current task id and
// defaults through to the backend.
func TestLabToolsDelegateToBackend(t *testing.T) {
	fake := &fakeLab{}
	ts := NewToolSet(nil, "")
	ts.SetLab(fake)
	ts.SetTaskID(42)

	byName := map[string]actool.CoreTool{}
	for _, tl := range ts.WorkerTools() {
		byName[tl.Name()] = tl
	}

	if res, err := byName["desync_probe"].Call(t.Context(), json.RawMessage(`{"url":"http://target.example"}`), nil); err != nil || res.IsError {
		t.Fatalf("desync_probe: err=%v res=%v", err, res)
	}
	if fake.desyncURL != "http://target.example" || fake.desyncTask != "42" {
		t.Fatalf("desync_probe args: url=%q task=%q", fake.desyncURL, fake.desyncTask)
	}

	if res, err := byName["index_cpg_sinks"].Call(t.Context(), json.RawMessage(`{"path":"lab/src"}`), nil); err != nil || res.IsError {
		t.Fatalf("index_cpg_sinks: err=%v res=%v", err, res)
	}
	if fake.cpgPath != "lab/src" || fake.cpgLanguage != "auto" || fake.cpgTask != "42" {
		t.Fatalf("index_cpg_sinks args: path=%q language=%q task=%q", fake.cpgPath, fake.cpgLanguage, fake.cpgTask)
	}

	if res, err := byName["launch_fuzz"].Call(t.Context(), json.RawMessage(`{"target":"lab/src/server"}`), nil); err != nil || res.IsError {
		t.Fatalf("launch_fuzz: err=%v res=%v", err, res)
	}
	if fake.fuzzTarget != "lab/src/server" || fake.fuzzKind != "afl++" || fake.fuzzTask != "42" {
		t.Fatalf("launch_fuzz args: target=%q kind=%q task=%q", fake.fuzzTarget, fake.fuzzKind, fake.fuzzTask)
	}

	if res, err := byName["list_cpg_sinks"].Call(t.Context(), json.RawMessage(`{}`), nil); err != nil || res.IsError {
		t.Fatalf("list_cpg_sinks: err=%v res=%v", err, res)
	}
}
