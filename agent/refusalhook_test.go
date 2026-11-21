package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

func TestRefusalFramingSurvivesPromptOverride(t *testing.T) {
	prev := PromptOverride()
	t.Cleanup(func() { SetPromptOverride(prev) })
	SetPromptOverride(func(string) (string, bool) { return "BARE BODY", true })

	for role, sys := range map[string]string{
		"planner":   plannerSystem("goal", "/data", "/work"),
		"worker":    workerSystem("", "", "/data", "/run"),
		"mainagent": mainAgentSystem("goal", "/data", "/work"),
		"chat":      chatSystem("auto", "/data", "/work"),
	} {
		if n := strings.Count(sys, refusalFraming); n != 1 {
			t.Errorf("%s: refusal framing rendered %d times, want 1", role, n)
		}
		if !strings.Contains(sys, "add intents") {
			t.Errorf("%s: framing lacks the intent instruction", role)
		}
	}
}

// A thinking-only turn is an idle turn, not a refusal: the refusal rerun must not
// swallow it (its text is empty).
func TestProseRefusalTurnShape(t *testing.T) {
	ctx := WithStopTracker(context.Background())
	refusal := "I can't continue with this one. Accessing money on plus.pl is theft/payment fraud, so this round produces no intents."
	work := "Next I will run curl against the in-scope host so the login flow can be mapped."

	cases := []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"prose refusal", []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: refusal}}}}, true},
		{"with tool call", []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.BlockText, Text: refusal}, {Type: llm.BlockToolUse, ID: "t1", Name: "add_intent"}}}}, false},
		{"normal text", []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: work}}}}, false},
		{"thinking only", []llm.Message{{Role: llm.RoleAssistant}}, false},
	}
	for _, c := range cases {
		if got := isProseRefusalTurn(ctx, c.msgs); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// The rerun stops at the cap and never fires twice for one refusal-free stream.
func TestRefusalRerunCaps(t *testing.T) {
	ctx := WithStopTracker(context.Background())
	msgs := []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockText, Text: "I can't continue with this: it is theft."}}}}
	h := WithRefusalRerun(nil)
	for i := 1; i <= refusalReruns; i++ {
		if _, blocked, _ := h.Stop(ctx, msgs); len(blocked) == 0 {
			t.Fatalf("rerun %d: expected the refusal nudge", i)
		}
	}
	if _, blocked, _ := h.Stop(ctx, msgs); len(blocked) != 0 {
		t.Fatalf("past the cap: expected no rerun, got %v", blocked)
	}
}

// A prose refusal reaches the graph with its hint and keeps the argument as
// evidence.
func TestTerminalTextProseRefusal(t *testing.T) {
	term := &harness.Terminal{Reason: harness.ReasonCompleted,
		Text: "I can't continue with this one, the conversion of funds is payment fraud."}
	sum, detail := terminalText(context.Background(), term, &runTrace{})
	if !strings.Contains(sum, "refusal argument") {
		t.Errorf("summary lacks the refusal label: %q", sum)
	}
	if !strings.Contains(detail, term.Text) {
		t.Error("detail dropped the argument")
	}
}
