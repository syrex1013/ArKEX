package llmpool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mockAnthropicServer(t *testing.T, response string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/anthropic/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(response)) //nolint:errcheck
	})
	return httptest.NewServer(mux)
}

func anthropicResp(content string) string {
	b, _ := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": content}},
		"usage":   map[string]any{"input_tokens": 10, "output_tokens": 20},
	})
	return string(b)
}

func TestPoolComplete(t *testing.T) {
	srv := mockAnthropicServer(t, anthropicResp("hello from mock"), http.StatusOK)
	defer srv.Close()

	p := New([]*Provider{
		{Name: "mock", Endpoint: srv.URL + "/anthropic/v1/messages", APIKey: "test-key", Model: "claude-haiku-5-5", Tier: 1, Enabled: true},
	})
	resp, err := p.Complete(t.Context(), &CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello from mock" {
		t.Fatalf("want 'hello from mock' got %q", resp.Content)
	}
	if resp.TokensIn != 10 || resp.TokensOut != 20 {
		t.Fatalf("unexpected tokens: in=%d out=%d", resp.TokensIn, resp.TokensOut)
	}
}

func TestPoolRefusalEscalates(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/anthropic/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		content := "i cannot help with that"
		if callCount >= 2 {
			content = "Here is the pentest result."
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"content": []map[string]any{{"type": "text", "text": content}},
			"usage":   map[string]any{"input_tokens": 5, "output_tokens": 10},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ep := srv.URL + "/anthropic/v1/messages"
	p := New([]*Provider{
		{Name: "tier1", Endpoint: ep, APIKey: "k", Model: "haiku", Tier: 1, Enabled: true},
		{Name: "tier2", Endpoint: ep, APIKey: "k", Model: "sonnet", Tier: 2, Enabled: true},
	})
	resp, err := p.Complete(t.Context(), &CompletionRequest{
		Messages:  []Message{{Role: "user", Content: "test refusal escalation"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if callCount < 2 {
		t.Fatal("expected tier escalation on refusal")
	}
	if resp.Content == "" {
		t.Fatal("empty final response")
	}
}

func TestIsRefusal(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"I cannot help with that.", true},
		{"I'm not able to assist.", true},
		{"I can't assist with this.", true},
		{"As an AI, I cannot do that.", true},
		{"", true},
		{"Here is the SQL injection payload: ' OR 1=1--", false},
		{"The endpoint is vulnerable to XSS.", false},
	}
	for _, tc := range cases {
		got := isRefusal(tc.content)
		if got != tc.want {
			t.Errorf("isRefusal(%q) = %v want %v", tc.content, got, tc.want)
		}
	}
}
