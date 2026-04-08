package grok

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
)

const (
	xaiBaseURL      = "https://api.x.ai/v1/responses"
	defaultModel    = "grok-3-fast" // Cheapest model: $0.20/M in, $0.50/M out
	requestTimeout  = 30 * time.Second
)

// Client wraps the xAI Responses API with the x_search tool enabled.
type Client struct {
	apiKey string
	http   *http.Client
	logger *slog.Logger
}

// NewClient creates a Grok client. apiKey comes from GROK_API_KEY env var.
func NewClient(apiKey string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: requestTimeout},
		logger: logger,
	}
}

// XInsight is a single insight extracted from X/Twitter posts.
type XInsight struct {
	Sentiment   string   `json:"sentiment"`    // "bullish", "bearish", "neutral", "mixed"
	Summary     string   `json:"summary"`      // Synthesized summary of what people are saying
	KeyThemes   []string `json:"key_themes"`   // Main topics / angles people are discussing
	BullPoints  []string `json:"bull_points"`  // Arguments for YES / why it will happen
	BearPoints  []string `json:"bear_points"`  // Arguments for NO / why it won't happen
	NotablePosts []NotablePost `json:"notable_posts"` // Specific high-signal posts
	Hashtags    []string `json:"hashtags"`     // Top hashtags found
	SearchedAt  time.Time `json:"searched_at"`
}

// NotablePost is a specific X post worth highlighting.
type NotablePost struct {
	Author  string `json:"author"`
	Content string `json:"content"`
	URL     string `json:"url,omitempty"`
	Why     string `json:"why"` // Why this post is notable
}

// responsesRequest is the body for POST /v1/responses
type responsesRequest struct {
	Model string        `json:"model"`
	Input []inputMsg    `json:"input"`
	Tools []tool        `json:"tools"`
}

type inputMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tool struct {
	Type string `json:"type"`
}

// responsesReply is the top-level xAI response envelope.
type responsesReply struct {
	Output []outputBlock `json:"output"`
	Error  *apiError     `json:"error,omitempty"`
}

type outputBlock struct {
	Type    string        `json:"type"`
	Content []contentPart `json:"content,omitempty"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// SearchXForMarket queries X/Twitter for sentiment and insights about a prediction
// market question. Returns structured insights extracted by Grok.
func (c *Client) SearchXForMarket(ctx context.Context, marketQuestion, marketCategory string) (*XInsight, error) {
	prompt := buildXSearchPrompt(marketQuestion, marketCategory)

	reqBody := responsesRequest{
		Model: defaultModel,
		Input: []inputMsg{
			{Role: "user", Content: prompt},
		},
		Tools: []tool{
			{Type: "x_search"},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xaiBaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	c.logger.Info("grok x_search", slog.String("market", truncate(marketQuestion, 80)))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xAI API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var reply responsesReply
	if err := json.Unmarshal(respBytes, &reply); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if reply.Error != nil {
		return nil, fmt.Errorf("xAI error: %s", reply.Error.Message)
	}

	// Extract the final text output (last message block with type=message)
	rawText := extractText(reply)
	if rawText == "" {
		return nil, fmt.Errorf("empty response from Grok")
	}

	insight, err := parseInsightFromText(rawText, marketQuestion)
	if err != nil {
		// Fallback: return the raw text as the summary so the user still gets value
		c.logger.Warn("could not parse structured insight, returning raw text", slog.String("err", err.Error()))
		return &XInsight{
			Sentiment:  "unknown",
			Summary:    rawText,
			SearchedAt: time.Now().UTC(),
		}, nil
	}

	insight.SearchedAt = time.Now().UTC()
	return insight, nil
}

// expertHandles returns a list of high-signal Twitter accounts for each category.
// These are injected into the search prompt so Grok prioritises expert voices.
func expertHandles(category string) []string {
	switch category {
	case "Politics":
		return []string{
			"@NateSilver538",  // probabilistic election forecasting
			"@Nate_Cohn",      // NYT Upshot polling analyst
			"@SabatosCV",      // Larry Sabato's Crystal Ball
			"@Polymarket",     // prediction market price signal
			"@PredictIt",      // prediction market price signal
			"@RealClearNews",  // polling aggregator
		}
	case "Crypto":
		return []string{
			"@lookonchain",    // on-chain whale tracking
			"@WuBlockchain",   // crypto news
			"@CryptoQuant_ki", // on-chain analytics
			"@DocumentingBTC", // Bitcoin milestone tracker
			"@WhalePanda",     // market analysis
			"@MacroScope__",   // crypto macro
		}
	case "Macroeconomics":
		return []string{
			"@charliebilello", // economic indicator charts
			"@KobeissiLetter", // macro analysis
			"@elerianm",       // Mohamed El-Erian
			"@NickTimiraos",   // WSJ Fed reporter
			"@GunjanJS",       // Bloomberg rates/macro
		}
	case "Corporate":
		return []string{
			"@DavidFaber",     // CNBC M&A reporter
			"@herbgreenberg",  // investigative financial journalism
			"@PeterEliades",   // institutional analysis
			"@WSJmarkets",     // WSJ markets desk
		}
	case "Sports (League)", "Sports (Qualification)":
		return []string{
			"@OptaJoe",        // soccer stats & predictions
			"@ESPNStatsInfo",  // ESPN statistical analysis
			"@FBref",          // football reference data
			"@SquawkaNFL",     // NFL analytics
			"@StatsPerform",   // sports analytics
		}
	default:
		return nil
	}
}

// buildXSearchPrompt creates the prompt that instructs Grok to search X and return
// structured JSON we can parse. category should be the detected analysis category label
// (e.g. "Politics", "Crypto") so expert handles can be injected.
func buildXSearchPrompt(question, category string) string {
	catHint := ""
	if category != "" {
		catHint = fmt.Sprintf(" This market is in the '%s' category.", category)
	}

	expertSection := ""
	if handles := expertHandles(category); len(handles) > 0 {
		expertSection = fmt.Sprintf(`
4. Specifically check posts from these high-signal accounts for this category: %s`,
			strings.Join(handles, ", "))
	}

	return fmt.Sprintf(`Search X (Twitter) for discussion about this prediction market question:

"%s"%s

Use x_search to find relevant recent posts. Search for:
1. The key entities/people/events mentioned in the question
2. Related hashtags and keywords people would use to discuss this
3. Posts from analysts, journalists, or domain experts relevant to this topic%s

After searching, respond ONLY with a JSON object in this exact format (no markdown, no extra text):
{
  "sentiment": "bullish|bearish|neutral|mixed",
  "summary": "2-3 sentence synthesis of what people on X are saying about this",
  "key_themes": ["theme1", "theme2", "theme3"],
  "bull_points": ["reason YES will happen", "another bull argument"],
  "bear_points": ["reason NO / it won't happen", "another bear argument"],
  "notable_posts": [
    {"author": "@handle", "content": "post content summary", "url": "post URL if available", "why": "why this post is notable/high-signal"},
    {"author": "@handle", "content": "post content summary", "url": "", "why": "insider/expert view"}
  ],
  "hashtags": ["#hashtag1", "#hashtag2"]
}

Focus on actionable signal: what do people with real knowledge of this topic believe will happen?`, question, catHint, expertSection)
}

// extractText pulls the final assistant text from the Responses API output array.
func extractText(reply responsesReply) string {
	// Walk output blocks in reverse to get the last message
	for i := len(reply.Output) - 1; i >= 0; i-- {
		block := reply.Output[i]
		if block.Type != "message" {
			continue
		}
		for _, part := range block.Content {
			if part.Type == "output_text" && part.Text != "" {
				return part.Text
			}
		}
	}
	return ""
}

// parseInsightFromText tries to extract a JSON XInsight from Grok's text response.
// Grok sometimes wraps JSON in markdown fences — we strip those.
func parseInsightFromText(text, question string) (*XInsight, error) {
	// Strip markdown code fences if present
	cleaned := text
	if idx := strings.Index(cleaned, "```json"); idx != -1 {
		cleaned = cleaned[idx+7:]
		if end := strings.Index(cleaned, "```"); end != -1 {
			cleaned = cleaned[:end]
		}
	} else if idx := strings.Index(cleaned, "```"); idx != -1 {
		cleaned = cleaned[idx+3:]
		if end := strings.Index(cleaned, "```"); end != -1 {
			cleaned = cleaned[:end]
		}
	}
	cleaned = strings.TrimSpace(cleaned)

	// Find JSON object boundaries
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON object found in response")
	}
	cleaned = cleaned[start : end+1]

	var insight XInsight
	if err := json.Unmarshal([]byte(cleaned), &insight); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w", err)
	}
	return &insight, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
