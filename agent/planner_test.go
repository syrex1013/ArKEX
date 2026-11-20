package agent

import (
	"testing"
)

func testPlanner() *Planner {
	return NewPlanner(nil, DefaultConfig())
}

func TestDetectStagnation(t *testing.T) {
	p := testPlanner()

	// 3 hypotheses with same 30-char prefix → stagnation
	same := "SQL injection via login form xyz"[:30]
	stagnant := []*HypothesisNode{
		{Title: same + "attempt 1"},
		{Title: same + "attempt 2"},
		{Title: same + "attempt 3"},
	}
	if !p.detectStagnation(stagnant) {
		t.Fatal("expected stagnation detected")
	}

	// Different titles — no stagnation
	diverse := []*HypothesisNode{
		{Title: "SQL injection via login form"},
		{Title: "XSS in search parameter"},
		{Title: "IDOR on user profile endpoint"},
	}
	if p.detectStagnation(diverse) {
		t.Fatal("unexpected stagnation on diverse hypotheses")
	}

	// Exactly 2 matches — not stagnant yet
	borderline := []*HypothesisNode{
		{Title: same + "v1"},
		{Title: same + "v2"},
		{Title: "completely different hypothesis here"},
	}
	if p.detectStagnation(borderline) {
		t.Fatal("2 matches should not trigger stagnation (need ≥3)")
	}
}

func TestDetectStagnationEmptyList(t *testing.T) {
	p := testPlanner()
	if p.detectStagnation(nil) {
		t.Fatal("empty list should not stagnate")
	}
	if p.detectStagnation([]*HypothesisNode{}) {
		t.Fatal("empty list should not stagnate")
	}
}

func TestDetectStagnationShortTitles(t *testing.T) {
	p := testPlanner()
	// Titles shorter than 30 chars — prefix is the full title
	short := []*HypothesisNode{
		{Title: "XSS"},
		{Title: "XSS"},
		{Title: "XSS"},
	}
	if !p.detectStagnation(short) {
		t.Fatal("3 identical short titles should stagnate")
	}
}
