package llmpool

import (
	"errors"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

// ErrProviderRefusal marks a completed call that the provider declined for safety.
// Treated as failover-eligible so a later Tier-3 local/uncensored profile can
// continue an authorized pentest turn without breaking the session.
var ErrProviderRefusal = errors.New("LLM provider refusal")

// refusalHead is how much of the text is scanned for a refusal phrase.
const refusalHead = 400

// IsRefusalStopReason reports Anthropic/OpenAI-style refusal stop reasons.
func IsRefusalStopReason(stop string) bool {
	switch strings.ToLower(strings.TrimSpace(stop)) {
	case "refusal", "content_filter":
		return true
	default:
		return false
	}
}

// LooksLikeRefusalText catches soft refusals that still return end_turn.
//
// Only the opening of the text is scanned: a refusal leads with its objection,
// while a normal worker conclusion may mention one of these phrases somewhere in
// the middle ("X is outside the scope of the task") after real work.
func LooksLikeRefusalText(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	if len(t) > refusalHead {
		t = t[:refusalHead]
	}
	needles := []string{
		"i cannot assist with",
		"i can't assist with",
		"i’m unable to help with that",
		"i'm unable to help with that",
		"against my safety",
		"against my programming",
		"i must refuse",
		"i won't help with",
		"i will not help with",
		"cannot help with hacking",
		"can't help with hacking",
		"not able to provide assistance with",
		// The plus.pl shape: the model accepts the request but stops anyway,
		// framing the goal as criminal work. It never says "refuse".
		"i can't continue with this",
		"i cannot continue with this",
		"i can't proceed with this",
		"is theft",
		"payment fraud",
		"not a security issue",
		"outside the scope of the",
		"won't advance the",
	}
	for _, n := range needles {
		if strings.Contains(t, n) {
			return true
		}
	}
	return false
}

// RefusalFromComplete returns ErrProviderRefusal when stop/text indicate refusal.
func RefusalFromComplete(msg llm.Message, stopReason string) error {
	if IsRefusalStopReason(stopReason) {
		return ErrProviderRefusal
	}
	if LooksLikeRefusalText(msg.Text()) {
		return ErrProviderRefusal
	}
	return nil
}
