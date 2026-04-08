package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/badgateway/poly/internal/scanner"
)

const (
	claudeBaseURL             = "https://api.anthropic.com"
	claudeMessagesPath        = "/v1/messages"
	claudeModel               = "claude-haiku-4-5-20251001" // Latest Claude Haiku (~$0.005 per market)
	conditionParserTimeout    = 10 * time.Second
	conditionParserMaxTokens  = 1024
)

// ParsedCondition holds the analysis of a market's resolution criteria
type ParsedCondition struct {
	TriggerConditions string `json:"trigger_conditions"` // Exact conditions for YES/NO resolution
	ResolutionSource  string `json:"resolution_source"`  // Who/what determines the outcome
	EdgeCases         string `json:"edge_cases"`         // Scenarios where event happens but market resolves differently
	KeyDates          string `json:"key_dates"`          // Important deadlines and cutoffs
	AmbiguityRisk     string `json:"ambiguity_risk"`     // low/medium/high — could reasonable people disagree?
	RawResponse       string `json:"raw_response,omitempty"` // Full Claude response for debugging
	Error             string `json:"error,omitempty"`    // Error message if parsing failed
}

// ConditionParser analyzes market resolution criteria using Claude Haiku
type ConditionParser struct {
	apiKey string
	client *http.Client
	logger *slog.Logger
}

// NewConditionParser creates a new condition parser with the given API key
func NewConditionParser(apiKey string, logger *slog.Logger) *ConditionParser {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConditionParser{
		apiKey: apiKey,
		client: &http.Client{Timeout: conditionParserTimeout},
		logger: logger,
	}
}

// claudeRequest is the POST body for the Claude API
type claudeRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Messages  []claudeMsg   `json:"messages"`
	System    string        `json:"system"`
}

// claudeMsg represents a message in the conversation
type claudeMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the parsed response from Claude API
type claudeResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Content []claudeContent `json:"content"`
	Error   *claudeError   `json:"error,omitempty"`
}

// claudeContent is a single content block in the response
type claudeContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// claudeError represents an API error
type claudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ParseConditions analyzes a market's resolution criteria using Claude Haiku
func (cp *ConditionParser) ParseConditions(ctx context.Context, sig scanner.Signal) (*ParsedCondition, error) {
	result := &ParsedCondition{}

	// Handle empty descriptions
	description := sig.Market.Description
	if description == "" {
		result.Error = "No resolution criteria provided by market"
		return result, nil
	}

	// Build the condition analysis prompt
	prompt := buildConditionPrompt(sig)

	// Call Claude API
	req := claudeRequest{
		Model:     claudeModel,
		MaxTokens: conditionParserMaxTokens,
		System: `You are an expert prediction market analyst specializing in resolution trap detection.
Analyze market resolution criteria to identify exact trigger conditions, resolution sources, and edge cases.

Return ONLY a JSON object. Every value MUST be a plain string — no arrays, no nested objects.
Use newlines within strings to separate multiple items.

Fields:
- trigger_conditions: "YES: [exact condition]\nNO: [exact condition]" — plain string
- resolution_source: Who or what entity determines the outcome — plain string
- edge_cases: Each trap on its own line, separated by \n — plain string, NOT an array
- key_dates: Each date on its own line, separated by \n — plain string, NOT an array
- ambiguity_risk: Exactly one of "low", "medium", or "high"

Example format:
{"trigger_conditions":"YES: X is officially announced as winner by NBA\nNO: Any other outcome","resolution_source":"Official NBA.com announcement","edge_cases":"Award delayed past deadline → resolves NO\nCo-winners → unclear resolution","key_dates":"2026-05-18: Market resolution date\n2026-12-31: Hard deadline","ambiguity_risk":"medium"}

No markdown, no code blocks, no explanation. ONLY the JSON object.`,
		Messages: []claudeMsg{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	// Marshal request
	reqBody, err := json.Marshal(req)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to marshal request: %v", err)
		return result, err
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(reqBody))
	if err != nil {
		result.Error = fmt.Sprintf("Failed to create request: %v", err)
		return result, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", cp.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	// Execute request
	resp, err := cp.client.Do(httpReq)
	if err != nil {
		result.Error = fmt.Sprintf("API request failed: %v", err)
		return result, err
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to read response: %v", err)
		return result, err
	}

	// Check for API errors
	if resp.StatusCode != http.StatusOK {
		var apiErr claudeResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != nil {
			result.Error = fmt.Sprintf("API error (%d): %s", resp.StatusCode, apiErr.Error.Message)
			return result, fmt.Errorf("API error: %s", apiErr.Error.Message)
		}
		result.Error = fmt.Sprintf("API error (%d): %s", resp.StatusCode, string(respBody))
		return result, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	// Parse Claude response
	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		result.Error = fmt.Sprintf("Failed to parse response: %v", err)
		return result, err
	}

	// Extract text content
	if len(claudeResp.Content) == 0 {
		result.Error = "No content in response"
		return result, fmt.Errorf("no content in response")
	}

	responseText := claudeResp.Content[0].Text
	result.RawResponse = responseText

	// Strip markdown code blocks if present
	jsonText := responseText
	if strings.HasPrefix(jsonText, "```json") {
		jsonText = strings.TrimPrefix(jsonText, "```json")
		if idx := strings.LastIndex(jsonText, "```"); idx != -1 {
			jsonText = jsonText[:idx]
		}
	} else if strings.HasPrefix(jsonText, "```") {
		jsonText = strings.TrimPrefix(jsonText, "```")
		if idx := strings.LastIndex(jsonText, "```"); idx != -1 {
			jsonText = jsonText[:idx]
		}
	}
	jsonText = strings.TrimSpace(jsonText)

	// Parse into generic map first — handles both flat strings and nested objects
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		result.Error = fmt.Sprintf("Failed to parse condition JSON: %v — raw: %s", err, jsonText)
		cp.logger.Warn("Failed to parse condition response",
			slog.String("market_id", sig.Market.ID),
			slog.String("error", err.Error()),
			slog.String("response", responseText))
		return result, nil // Return with error field set
	}

	// Extract each field, converting objects to JSON strings if needed
	result.TriggerConditions = extractField(raw, "trigger_conditions")
	result.ResolutionSource = extractField(raw, "resolution_source")
	result.EdgeCases = extractField(raw, "edge_cases")
	result.KeyDates = extractField(raw, "key_dates")
	result.AmbiguityRisk = extractField(raw, "ambiguity_risk")
	result.Error = ""

	cp.logger.Debug("Condition parsing complete",
		slog.String("market_id", sig.Market.ID),
		slog.String("ambiguity_risk", result.AmbiguityRisk))

	return result, nil
}

// buildConditionPrompt constructs the prompt for condition analysis
func buildConditionPrompt(sig scanner.Signal) string {
	var sb bytes.Buffer

	sb.WriteString("Analyze this prediction market's resolution criteria:\n\n")
	sb.WriteString("**Market Question:** " + sig.Market.Question + "\n\n")
	sb.WriteString("**Resolution Criteria:**\n")
	sb.WriteString(sig.Market.Description + "\n\n")
	sb.WriteString("**Additional Context:**\n")
	sb.WriteString("- Market resolves on: " + sig.Market.EndDate.Format("2006-01-02") + "\n")
	sb.WriteString("- This is a " + detectCategoryLabel(sig) + " market\n\n")
	sb.WriteString("Identify:\n")
	sb.WriteString("1. EXACT trigger conditions — what specifically must happen for YES vs NO\n")
	sb.WriteString("2. Resolution source — who/what entity determines the outcome\n")
	sb.WriteString("3. Edge cases — scenarios where the underlying event occurs but market resolves differently\n")
	sb.WriteString("4. Key dates — important deadlines or time cutoffs in the rules\n")
	sb.WriteString("5. Ambiguity risk — assess the likelihood of disputed resolution (low/medium/high)\n")

	return sb.String()
}

// detectCategoryLabel returns a human-readable category label
func detectCategoryLabel(sig scanner.Signal) string {
	cat := DetectCategory(sig.Market.Question)
	return cat.Label()
}

// extractField extracts a map value as a string.
// If the value is an object or array, marshals it to a compact JSON string
// so we don't lose data when Claude returns nested structures instead of flat strings.
func extractField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		// Object or array — marshal to JSON string rather than discarding
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Sprintf("%v", s)
		}
		return string(b)
	}
}
