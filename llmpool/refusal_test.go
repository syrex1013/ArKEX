package llmpool

import (
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestIsRefusalStopReason(t *testing.T) {
	if !IsRefusalStopReason("refusal") || !IsRefusalStopReason("content_filter") {
		t.Fatal("expected refusal stops")
	}
	if IsRefusalStopReason("end_turn") {
		t.Fatal("end_turn is not a refusal")
	}
}

func TestLooksLikeRefusalText(t *testing.T) {
	if !LooksLikeRefusalText("Sorry, I cannot assist with that request.") {
		t.Fatal("expected soft refusal")
	}
	if LooksLikeRefusalText("Next I will run curl against the in-scope host.") {
		t.Fatal("false positive")
	}
}

func TestRefusalFromComplete(t *testing.T) {
	msg := llm.UserText("x") // role irrelevant; Text() empty for user with no content blocks of refusal
	if err := RefusalFromComplete(msg, "refusal"); err == nil {
		t.Fatal("want ErrProviderRefusal")
	}
}
