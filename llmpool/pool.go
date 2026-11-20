// Package llmpool implements multi-tier LLM routing with refusal detection
// and automatic failover to Tier 3 (local/uncensored) models.
package llmpool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Tier constants
const (
	TierFast      = 1 // Recon triage, JS parsing, compaction
	TierFrontier  = 2 // Planner strategy, exploit chaining, verification
	TierUncensored = 3 // Local Ollama/vLLM, refusal failover
)

// Pool routes completions across Tier 1/2/3 providers with failover.
type Pool struct {
	providers []*Provider
	httpCli   *http.Client
}

type Provider struct {
	ID       string
	Name     string
	Tier     int
	Endpoint string
	Model    string
	APIKey   string
	Enabled  bool
}

func New(providers []*Provider) *Pool {
	return &Pool{
		providers: providers,
		httpCli:   &http.Client{Timeout: 120 * time.Second},
	}
}

// DefaultProviders returns a standard 3-tier configuration.
func DefaultProviders(anthropicKey, openaiKey, ollamaURL string) []*Provider {
	return []*Provider{
		{
			ID: "claude-haiku", Tier: TierFast,
			Endpoint: "https://api.anthropic.com/v1/messages",
			Model: "claude-haiku-5-5", APIKey: anthropicKey, Enabled: true,
		},
		{
			ID: "claude-sonnet", Tier: TierFrontier,
			Endpoint: "https://api.anthropic.com/v1/messages",
			Model: "claude-sonnet-5-5", APIKey: anthropicKey, Enabled: true,
		},
		{
			ID: "ollama-local", Tier: TierUncensored,
			Endpoint: ollamaURL + "/api/chat",
			Model: "llama3.2", Enabled: ollamaURL != "",
		},
	}
}

// CompletionRequest is a request to complete an LLM conversation.
type CompletionRequest struct {
	SessionID string
	IntentID  string
	Tier      int
	Messages  []Message
	MaxTokens int
	System    string
}

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionResponse is the LLM completion result.
type CompletionResponse struct {
	Content    string
	TokensIn   int
	TokensOut  int
	Model      string
	ProviderID string
	Refusal    bool
}

// Complete routes a completion request through the tier hierarchy with 5-layer retry.
func (p *Pool) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	// Find candidates for the requested tier
	for tier := req.Tier; tier <= TierUncensored; tier++ {
		for _, prov := range p.providers {
			if prov.Tier != tier || !prov.Enabled {
				continue
			}
			resp, err := p.completeWithRetry(ctx, prov, req, 5)
			if err != nil {
				slog.Warn("provider failed", "provider", prov.ID, "err", err)
				continue
			}
			if isRefusal(resp.Content) {
				slog.Warn("refusal detected, escalating tier",
					"provider", prov.ID, "tier", tier)
				resp.Refusal = true
				// Try next tier
				break
			}
			return resp, nil
		}
	}
	return nil, fmt.Errorf("all providers failed or refused")
}

// completeWithRetry attempts completion up to maxRetries times (5-layer retry stack).
func (p *Pool) completeWithRetry(ctx context.Context, prov *Provider, req *CompletionRequest, maxRetries int) (*CompletionResponse, error) {
	var lastErr error
	delays := []time.Duration{0, 1*time.Second, 2*time.Second, 4*time.Second, 8*time.Second}
	for i := 0; i < maxRetries; i++ {
		if i > 0 && i < len(delays) {
			select {
			case <-time.After(delays[i]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		resp, err := p.doComplete(ctx, prov, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// Don't retry context cancellations
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// doComplete executes a single completion call to the provider.
func (p *Pool) doComplete(ctx context.Context, prov *Provider, req *CompletionRequest) (*CompletionResponse, error) {
	start := time.Now()

	// Build provider-specific request body
	var body []byte
	var err error
	if strings.Contains(prov.Endpoint, "anthropic") {
		body, err = p.buildAnthropicRequest(req, prov.Model)
	} else {
		body, err = p.buildOllamaRequest(req, prov.Model)
	}
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", prov.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if prov.APIKey != "" {
		if strings.Contains(prov.Endpoint, "anthropic") {
			httpReq.Header.Set("x-api-key", prov.APIKey)
			httpReq.Header.Set("anthropic-version", "2023-06-01")
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+prov.APIKey)
		}
	}

	resp, err := p.httpCli.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d from %s", resp.StatusCode, prov.ID)
	}

	latency := time.Since(start)
	_ = latency

	if strings.Contains(prov.Endpoint, "anthropic") {
		return p.parseAnthropicResponse(resp, prov.ID, prov.Model)
	}
	return p.parseOllamaResponse(resp, prov.ID, prov.Model)
}

func (p *Pool) buildAnthropicRequest(req *CompletionRequest, model string) ([]byte, error) {
	type anthMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type anthReq struct {
		Model     string    `json:"model"`
		MaxTokens int       `json:"max_tokens"`
		System    string    `json:"system,omitempty"`
		Messages  []anthMsg `json:"messages"`
	}
	ar := anthReq{Model: model, MaxTokens: req.MaxTokens, System: req.System}
	for _, m := range req.Messages {
		if m.Role != "system" {
			ar.Messages = append(ar.Messages, anthMsg{Role: m.Role, Content: m.Content})
		} else if ar.System == "" {
			ar.System = m.Content
		}
	}
	return json.Marshal(ar)
}

func (p *Pool) parseAnthropicResponse(resp *http.Response, provID, model string) (*CompletionResponse, error) {
	var ar struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return nil, err
	}
	var text string
	for _, c := range ar.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	// Detect safety refusals via stop_reason
	if ar.StopReason == "content_filtered" || ar.StopReason == "refusal" {
		text = ""
	}
	return &CompletionResponse{
		Content:    text,
		TokensIn:   ar.Usage.InputTokens,
		TokensOut:  ar.Usage.OutputTokens,
		Model:      model,
		ProviderID: provID,
	}, nil
}

func (p *Pool) buildOllamaRequest(req *CompletionRequest, model string) ([]byte, error) {
	type ollamaReq struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Stream   bool      `json:"stream"`
	}
	or := ollamaReq{Model: model, Stream: false, Messages: req.Messages}
	return json.Marshal(or)
}

func (p *Pool) parseOllamaResponse(resp *http.Response, provID, model string) (*CompletionResponse, error) {
	var or struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		EvalCount  int `json:"eval_count"`
		PromptEvalCount int `json:"prompt_eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return nil, err
	}
	return &CompletionResponse{
		Content:    or.Message.Content,
		TokensIn:   or.PromptEvalCount,
		TokensOut:  or.EvalCount,
		Model:      model,
		ProviderID: provID,
	}, nil
}

// isRefusal detects safety refusal phrases in LLM responses.
func isRefusal(content string) bool {
	content = strings.ToLower(content)
	refusalPhrases := []string{
		"i cannot help with",
		"i'm not able to",
		"i can't assist with",
		"as an ai, i cannot",
		"i don't feel comfortable",
		"this request violates",
		"i'm designed to be",
		"i must decline",
		"harmful or illegal",
		"content_filter",
	}
	for _, phrase := range refusalPhrases {
		if strings.Contains(content, phrase) {
			return true
		}
	}
	return content == "" // empty = likely filtered
}

// HealthCheck checks all providers and returns a summary.
func (p *Pool) HealthCheck(ctx context.Context) []ProviderHealth {
	var results []ProviderHealth
	for _, prov := range p.providers {
		h := ProviderHealth{
			ProviderID: prov.ID,
			Tier:       prov.Tier,
			Model:      prov.Model,
			Enabled:    prov.Enabled,
		}
		if !prov.Enabled {
			results = append(results, h)
			continue
		}
		start := time.Now()
		req := &CompletionRequest{
			Tier:      prov.Tier,
			Messages:  []Message{{Role: "user", Content: "Reply with one word: OK"}},
			MaxTokens: 10,
		}
		resp, err := p.doComplete(ctx, prov, req)
		h.LatencyMS = int(time.Since(start).Milliseconds())
		if err != nil {
			h.Error = err.Error()
		} else {
			h.Healthy = true
			h.Refusal = isRefusal(resp.Content)
		}
		results = append(results, h)
	}
	return results
}

type ProviderHealth struct {
	ProviderID string
	Tier       int
	Model      string
	Enabled    bool
	Healthy    bool
	Refusal    bool
	LatencyMS  int
	Error      string
}
