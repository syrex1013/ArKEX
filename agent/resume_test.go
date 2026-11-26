package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func checkpointFixture() *db.IntentCheckpoint {
	return &db.IntentCheckpoint{
		ID:        9,
		TaskID:    1,
		IntentID:  2,
		AgentName: "web#3",
		Attempt:   3,
		Phase:     "Bash",
		NextStep:  "Enumerate /admin on 10.0.0.5",
		Done:      []string{"Port scan of 10.0.0.5", "Found /login page"},
		Open:      []string{"Try default creds on /admin", "Check /api version"},
		Artifacts: []string{"/tmp/task/nmap.txt"},
		ToolCalls: 17,
	}
}

func TestResumeBlockNilLast(t *testing.T) {
	if got := renderResumeBlock(nil, 2, 3); got != "" {
		t.Fatalf("nil last rendered %q, want empty", got)
	}
}

func TestResumeBlockFull(t *testing.T) {
	got := renderResumeBlock(checkpointFixture(), 2, 3)
	for _, want := range []string{
		"Resume attempt 2/3.",
		"Last phase: Bash",
		"Done so far:",
		"- Port scan of 10.0.0.5",
		"- Found /login page",
		"Still open:",
		"- Try default creds on /admin",
		"- Check /api version",
		"Artifacts:",
		"- /tmp/task/nmap.txt",
		"Next step: Enumerate /admin on 10.0.0.5",
		"Continue from the checkpoint; do not repeat completed steps.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("block missing %q:\n%s", want, got)
		}
	}
}

func TestResumeBlockSkipsEmptySections(t *testing.T) {
	last := checkpointFixture()
	last.Done, last.Open, last.Artifacts, last.Phase = nil, nil, nil, ""
	got := renderResumeBlock(last, 1, 3)
	for _, unwanted := range []string{"Last phase", "Done so far", "Still open", "Artifacts"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("empty section %q rendered:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "Next step:") {
		t.Fatalf("next step lost:\n%s", got)
	}
}

func TestResumeBlockCapTrimsOpenFirst(t *testing.T) {
	last := checkpointFixture()
	last.Open = nil
	last.Done = nil
	// One long open item (over the cap alone) and a distinctive short done item.
	long := strings.Repeat("x", 2500)
	last.Open = []string{long}
	last.Done = []string{"done-keepsake"}
	got := renderResumeBlock(last, 1, 3)
	if len(got) > resumeBlockCap {
		t.Fatalf("block is %d chars, cap is %d", len(got), resumeBlockCap)
	}
	if !strings.Contains(got, "done-keepsake") {
		t.Fatalf("done item dropped before open items:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("x", 2000)) {
		t.Fatalf("long open item survived the cap:\n%s", got)
	}
}

func TestResumeBlockCapTrimsDoneWhenNoOpen(t *testing.T) {
	last := checkpointFixture()
	last.Open = nil
	last.Done = []string{"done-keepsake", strings.Repeat("y", 2500)}
	got := renderResumeBlock(last, 1, 3)
	if len(got) > resumeBlockCap {
		t.Fatalf("block is %d chars, cap is %d", len(got), resumeBlockCap)
	}
	if !strings.Contains(got, "done-keepsake") {
		t.Fatalf("kept item dropped instead of the long tail:\n%s", got)
	}
}
