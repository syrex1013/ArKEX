package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

func TestLLMTransitionReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "quota exhausted"},
		{"budget", fmt.Errorf("%w: estimate 1 exceeds configured budget 1", agent.ErrNoaBudget), "context window too small for the request"},
		{"window cause", fmt.Errorf("profile #18 context window cannot fit the request: boom"), "context window too small for the request"},
		{"streak", fmt.Errorf("profile #18 failed 3 consecutive calls (last: status 400)"), "failed consecutive calls"},
		{"billing", errors.New("openai: status 402: insufficient_quota"), "quota exhausted"},
		{"other", errors.New("unexpected EOF"), "unusable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := llmTransitionReason(c.err); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
