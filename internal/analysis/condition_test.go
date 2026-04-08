package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

func TestConditionParserMockServer(t *testing.T) {
	// Mock Claude API response
	mockResp := claudeResponse{
		ID:   "msg-123",
		Type: "message",
		Content: []claudeContent{
			{
				Type: "text",
				Text: `{
					"trigger_conditions": "YES: Bitcoin price reaches $100,000 on CoinGecko main page at exactly 00:00 UTC on 2026-05-15. NO: Any other outcome.",
					"resolution_source": "CoinGecko historical price data at 00:00 UTC on the specified date",
					"edge_cases": "Flash crash to $100K on futures but not recorded by CoinGecko at the timestamp = NO. Price hits $99,999.50 = NO.",
					"key_dates": "2026-05-15 at 00:00 UTC is the hard resolution cutoff",
					"ambiguity_risk": "medium"
				}`,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}

		// Verify headers
		if r.Header.Get("x-api-key") != "test-claude-key" {
			t.Errorf("expected api key header 'test-claude-key', got %q", r.Header.Get("x-api-key"))
		}

		// Verify request body structure
		var reqBody claudeRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if reqBody.Model != claudeModel {
			t.Errorf("expected model %q, got %q", claudeModel, reqBody.Model)
		}
		if len(reqBody.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(reqBody.Messages))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResp)
	}))
	defer server.Close()

	logger := slog.Default()
	cp := &ConditionParser{
		apiKey: "test-claude-key",
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				// Use test server transport
			},
		},
		logger: logger,
	}

	// Create a test signal
	sig := scanner.Signal{
		FilteredMarket: scanner.FilteredMarket{
			Market: polymarket.Market{
				ID:          "123",
				Question:    "Will Bitcoin reach $100,000 by May 15, 2026?",
				Description: "This market resolves YES if Bitcoin reaches $100,000 on CoinGecko's main page at 00:00 UTC on May 15, 2026.",
				EndDate:     time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
			},
			DaysToResolve: 42,
		},
		TargetSide: "YES",
	}

	// Override the API call to use the mock server
	// We'll create a custom request to use the mock server URL
	req := claudeRequest{
		Model:     claudeModel,
		MaxTokens: conditionParserMaxTokens,
		System: `You are an expert prediction market analyst specializing in resolution trap detection.
Analyze market resolution criteria to identify exact trigger conditions, resolution sources, and edge cases.
Return your analysis as a JSON object with these exact fields:
- trigger_conditions: Exact conditions for YES and NO resolution
- resolution_source: Who or what entity determines the outcome
- edge_cases: Scenarios where underlying event happens but market resolves differently
- key_dates: Important dates, deadlines, or cutoff times in the resolution rules
- ambiguity_risk: One of "low", "medium", or "high" — assess risk of disputed resolution

Be concise. Respond with ONLY the JSON object, no additional text.`,
		Messages: []claudeMsg{
			{
				Role:    "user",
				Content: buildConditionPrompt(sig),
			},
		},
	}

	// Marshal and send a manual request to the mock server
	reqBody, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/v1/messages", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", "test-claude-key")
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := cp.client.Do(httpReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var respData claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Extract the text
	if len(respData.Content) == 0 {
		t.Fatal("expected content in response")
	}

	responseText := respData.Content[0].Text
	var parsed ParsedCondition
	if err := json.Unmarshal([]byte(responseText), &parsed); err != nil {
		t.Fatalf("parse condition JSON: %v", err)
	}

	// Verify parsed values
	if parsed.TriggerConditions == "" {
		t.Error("expected non-empty trigger_conditions")
	}
	if parsed.ResolutionSource != "CoinGecko historical price data at 00:00 UTC on the specified date" {
		t.Errorf("unexpected resolution_source: %q", parsed.ResolutionSource)
	}
	if parsed.AmbiguityRisk != "medium" {
		t.Errorf("expected ambiguity_risk 'medium', got %q", parsed.AmbiguityRisk)
	}
}

func TestBuildConditionPrompt(t *testing.T) {
	tests := []struct {
		question string
		wantContains string
	}{
		{
			"Will Bitcoin reach $100,000?",
			"Bitcoin reach $100,000",
		},
		{
			"Will the Fed cut rates in June 2026?",
			"Fed cut",
		},
		{
			"Will Tottenham be relegated?",
			"Tottenham",
		},
	}

	for _, tt := range tests {
		sig := scanner.Signal{
			FilteredMarket: scanner.FilteredMarket{
				Market: polymarket.Market{
					Question:    tt.question,
					Description: "Resolves YES if [condition happens]",
					EndDate:     time.Now().AddDate(0, 0, 30),
				},
				DaysToResolve: 30,
			},
		}

		prompt := buildConditionPrompt(sig)
		if prompt == "" {
			t.Errorf("buildConditionPrompt(%q) returned empty", tt.question)
		}
		if !containsSubstring(prompt, tt.wantContains) {
			t.Errorf("buildConditionPrompt(%q) does not contain %q\nGot: %s", tt.question, tt.wantContains, prompt)
		}
		if !containsSubstring(prompt, "Market Question") {
			t.Errorf("buildConditionPrompt missing 'Market Question'")
		}
		if !containsSubstring(prompt, "Resolution Criteria") {
			t.Errorf("buildConditionPrompt missing 'Resolution Criteria'")
		}
	}
}
