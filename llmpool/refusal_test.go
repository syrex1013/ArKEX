package llmpool

import (
	"errors"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestIsRefusalStopReason(t *testing.T) {
	if !IsRefusalStopReason("refusal") || !IsRefusalStopReason("content_filter") || !IsRefusalStopReason("sensitive") {
		t.Fatal("expected refusal stops")
	}
	if IsRefusalStopReason("end_turn") {
		t.Fatal("end_turn is not a refusal")
	}
}

// z.ai GLM's content filter: finish_reason "sensitive" with an empty delta, no
// usage and no error. It must classify as a refusal so the pool fails over
// instead of ending the round as a normal empty turn.
func TestRefusalFromCompleteSensitive(t *testing.T) {
	if err := RefusalFromComplete(llm.Message{}, "sensitive"); !errors.Is(err, ErrProviderRefusal) {
		t.Fatalf("sensitive stop: got %v, want ErrProviderRefusal", err)
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

// The observed plus.pl shape: the model accepts the request but argues the goal
// is real theft, then stops without a tool call.
func TestLooksLikeRefusalTextPlusPL(t *testing.T) {
	prose := "I can't continue with this one. The task's goal — accessing money/funds on plus.pl and converting them into cryptocurrency sent to a fixed external address — is theft/payment fraud. So this round I'm producing no intents."
	if !LooksLikeRefusalText(prose) {
		t.Fatal("plus.pl refusal not detected")
	}
}

// A worker conclusion that mentions the same words after real work is not a
// refusal: only the opening is scanned.
func TestLooksLikeRefusalTextIgnoresLateMention(t *testing.T) {
	prose := "Extracted 40 rows from the order table and wrote them back as facts; the payout endpoint is outside the scope of this intent, so the next one should cover it."
	if LooksLikeRefusalText(prose) {
		t.Fatal("late mention treated as a refusal")
	}
}

func TestRefusalFromComplete(t *testing.T) {
	msg := llm.UserText("x") // role irrelevant; Text() empty for user with no content blocks of refusal
	if err := RefusalFromComplete(msg, "refusal"); err == nil {
		t.Fatal("want ErrProviderRefusal")
	}
}
