package agent

import (
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

// A resumed run whose transcript still exists takes the "Continue." overwrite
// branch; the resume block must survive that overwrite and lead the launch input.
func TestResumeBlockSurvivesTranscriptContinuation(t *testing.T) {
	f := newSessionFixture(t)
	tx := transcript.NewStore(t.TempDir())
	cp := db.IntentCheckpoint{
		TaskID: f.taskID, IntentID: f.intentID, Attempt: 1, Phase: "Bash",
		NextStep: "verify the /data directory", Done: []string{"listed the root"},
	}
	if _, err := f.ts.DB().AddCheckpoint(cp); err != nil {
		t.Fatal(err)
	}

	// Seed the deterministic transcript so the second run resumes a conversation.
	seed := tx.NewWriter(WorkerSessionID(f.expID, f.intentID), "")
	seed.RecordMessage(llm.UserText("earlier launch"), llm.Usage{})
	seed.RecordMessage(llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("earlier turn")}}, llm.Usage{})
	if err := seed.Err(); err != nil {
		t.Fatal(err)
	}

	p := &sessionProbe{}
	w := NewWorker(p, "test-model", t.TempDir(), tx, 0, 3)
	w.SetResume(ResumeState{Attempt: 2, Max: 3, Last: &cp})
	if _, _, err := w.Execute(t.Context(), "work#1", f.expID, nil, f.ts, f.intent, passHooks{}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.msgs) == 0 {
		t.Fatal("no provider call")
	}
	// The launch message is the last user message of the first request.
	last := p.msgs[0][len(p.msgs[0])-1]
	got := last.Text()
	if !strings.HasPrefix(got, "\n\nResume attempt 2/3.") {
		t.Fatalf("launch input does not lead with the resume block:\n%s", got)
	}
	for _, want := range []string{"listed the root", "Next step: verify the /data directory", "Continue from the checkpoint"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resume block lost %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Continue.") {
		t.Fatalf("the continuation instruction was dropped:\n%s", got)
	}
}
