package llmrec

import (
	"encoding/json"
	"sync"

	"github.com/Autumn-27/artex/storage"
	"github.com/Autumn-27/norma/llm"
)

// sessionSeq tracks how many messages were last persisted per session so we
// only append new turns (kills O(N²) full-history rewrites).
var sessionSeq sync.Map // sessionID -> int

func lastSeq(session string) int {
	if v, ok := sessionSeq.Load(session); ok {
		return v.(int)
	}
	return 0
}

func setSeq(session string, n int) { sessionSeq.Store(session, n) }

// deltaRequestBody builds a compact request trace: system hash once + only
// messages after the previously recorded count for this session.
func deltaRequestBody(session string, req llm.CompletionRequest, thinkingType, reasoningEffort string) (body string, newPlain []byte, from, to int) {
	prev := lastSeq(session)
	msgs := req.Messages
	if prev > len(msgs) {
		prev = 0
	}
	newMsgs := msgs[prev:]
	from, to = prev, len(msgs)

	sysSHA := ""
	if len(req.System) > 0 {
		b, _ := json.Marshal(req.System)
		sysSHA = storage.SHA256Hex(b)
	}
	m := map[string]any{
		"delta":         true,
		"system_sha256": sysSHA,
		"msg_from":      from,
		"msg_to":        to,
		"messages":      newMsgs,
		"max_tokens":    req.MaxTokens,
	}
	effType := thinkingType
	if req.Thinking != "" {
		effType = req.Thinking
	}
	if effType != "" || reasoningEffort != "" {
		m["thinking"] = map[string]string{"type": effType, "effort": reasoningEffort}
	}
	if len(req.Tools) > 0 {
		names := make([]string, len(req.Tools))
		for i, t := range req.Tools {
			names[i] = t.Name
		}
		m["tools"] = names
		m["tools_count"] = len(req.Tools)
	}
	if req.Temperature != nil {
		m["temperature"] = *req.Temperature
	}
	raw, _ := json.Marshal(m)
	plain, _ := json.Marshal(newMsgs)
	return string(raw), plain, from, to
}
