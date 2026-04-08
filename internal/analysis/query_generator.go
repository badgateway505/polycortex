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
)

const (
	queryGeneratorTimeout   = 10 * time.Second
	queryGeneratorMaxTokens = 256  // Queries are short — no need for more
	summarizerModel         = "claude-sonnet-4-6" // Sonnet for quality summarisation
	summarizerMaxTokens     = 1024
	summarizerTimeout       = 20 * time.Second
)

// SearchQueries holds AI-generated search queries optimised for each engine.
type SearchQueries struct {
	Tavily string `json:"tavily"` // Keyword-focused — news, breaking events
	Exa    string `json:"exa"`    // Semantic — expert analysis, research, deep dives
}

// QueryGenerator uses Claude Haiku to generate optimal search queries
// for a given prediction market question.
type QueryGenerator struct {
	apiKey string
	client *http.Client
	logger *slog.Logger
}

// NewQueryGenerator creates a QueryGenerator backed by Claude Haiku.
func NewQueryGenerator(apiKey string, logger *slog.Logger) *QueryGenerator {
	return &QueryGenerator{
		apiKey: apiKey,
		client: &http.Client{Timeout: summarizerTimeout}, // sized for Sonnet summarize; Generate is fast via small prompt
		logger: logger,
	}
}

// Generate produces one Tavily query and one Exa query for the given market question.
// Single Haiku call (~$0.001). Falls back to the raw question if the call fails.
func (qg *QueryGenerator) Generate(ctx context.Context, question string) (*SearchQueries, error) {
	prompt := fmt.Sprintf(`Prediction market question: "%s"

Generate two search queries:
1. tavily_query — keyword search for breaking news and current events. Include specific names, dates, competitions. Short and precise. Example: "Tottenham Premier League relegation 2026 standings"
2. exa_query — semantic search for expert analysis, research, deep-dives, academic content. Phrase it as an intent or question. Example: "analysis Tottenham relegation probability 2025-26 Premier League survival"

Return ONLY JSON: {"tavily":"...","exa":"..."}`, question)

	req := claudeRequest{
		Model:     claudeModel,
		MaxTokens: queryGeneratorMaxTokens,
		System:    "You generate optimised web search queries for prediction market research. Return only valid JSON with tavily and exa fields. No explanation.",
		Messages:  []claudeMsg{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fallbackQueries(question), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(body))
	if err != nil {
		return fallbackQueries(question), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", qg.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := qg.client.Do(httpReq)
	if err != nil {
		qg.logger.Warn("query generator API call failed, using fallback", slog.String("err", err.Error()))
		return fallbackQueries(question), nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallbackQueries(question), nil
	}

	if resp.StatusCode != http.StatusOK {
		qg.logger.Warn("query generator API error, using fallback",
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBytes)))
		return fallbackQueries(question), nil
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBytes, &claudeResp); err != nil || len(claudeResp.Content) == 0 {
		return fallbackQueries(question), nil
	}

	text := claudeResp.Content[0].Text
	// Strip markdown fences if present
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i != -1 {
			text = text[i+1:]
		}
		if i := strings.LastIndex(text, "```"); i != -1 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
	}

	var queries SearchQueries
	if err := json.Unmarshal([]byte(text), &queries); err != nil {
		qg.logger.Warn("query generator JSON parse failed, using fallback",
			slog.String("raw", text),
			slog.String("err", err.Error()))
		return fallbackQueries(question), nil
	}

	if queries.Tavily == "" {
		queries.Tavily = question
	}
	if queries.Exa == "" {
		queries.Exa = question
	}

	qg.logger.Info("query generator complete",
		slog.String("tavily", queries.Tavily),
		slog.String("exa", queries.Exa))

	return &queries, nil
}

// fallbackQueries returns the raw market question for both engines.
// Used when the Haiku call fails so searches still run.
func fallbackQueries(question string) *SearchQueries {
	return &SearchQueries{Tavily: question, Exa: question}
}

// ArticleInsight holds one article's AI-extracted insight.
type ArticleInsight struct {
	Index   int    `json:"index"`
	Insight string `json:"insight"`
}

// DigestArticles sends all article texts to Haiku in one call and returns
// a per-article insight relevant to the market question. (~$0.002-0.005)
func (qg *QueryGenerator) DigestArticles(ctx context.Context, question string, articles []ArticleInput) []ArticleInsight {
	if len(articles) == 0 {
		return nil
	}

	var sb strings.Builder
	for i, a := range articles {
		if i >= 10 {
			break
		}
		sb.WriteString(fmt.Sprintf("[%d] Title: %s\n%s\n\n", i+1, a.Title, a.Text))
	}

	prompt := fmt.Sprintf(`Market question: "%s"

Articles:
%s

For each article, write ONE line extracting the key fact or insight relevant to the market question. Include specific numbers, dates, names. If the article has nothing relevant, write "NOT RELEVANT".

Return ONLY a JSON array: [{"index":1,"insight":"..."},{"index":2,"insight":"..."}]`, question, sb.String())

	req := claudeRequest{
		Model:     claudeModel, // Haiku — fast and cheap for extraction
		MaxTokens: 1024,
		System:    "You extract one key insight per article for prediction market research. Return valid JSON array only. No markdown.",
		Messages:  []claudeMsg{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", qg.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := qg.client.Do(httpReq)
	if err != nil {
		qg.logger.Warn("digest articles API call failed", slog.String("err", err.Error()))
		return nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		qg.logger.Warn("digest articles API error", slog.Int("status", resp.StatusCode), slog.String("body", string(respBytes)))
		return nil
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBytes, &claudeResp); err != nil || len(claudeResp.Content) == 0 {
		return nil
	}

	text := strings.TrimSpace(claudeResp.Content[0].Text)
	// Strip markdown fences
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i != -1 {
			text = text[i+1:]
		}
		if i := strings.LastIndex(text, "```"); i != -1 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
	}

	var insights []ArticleInsight
	if err := json.Unmarshal([]byte(text), &insights); err != nil {
		qg.logger.Warn("digest articles parse failed", slog.String("raw", text), slog.String("err", err.Error()))
		return nil
	}

	qg.logger.Info("digest articles complete", slog.Int("articles", len(insights)))
	return insights
}

// ArticleInput is a title+text pair for the digest call.
type ArticleInput struct {
	Title string
	Text  string
}

// Summarize distills all search snippets into key facts that directly affect
// market resolution. Uses Sonnet for quality (~$0.01 per call).
// Returns empty string on failure — caller shows raw results as fallback.
func (qg *QueryGenerator) Summarize(ctx context.Context, question string, snippets []string) string {
	if len(snippets) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, s := range snippets {
		if i >= 15 {
			break
		}
		sb.WriteString(fmt.Sprintf("[%d] %s\n\n", i+1, s))
	}

	prompt := fmt.Sprintf(`Market question: "%s"

Search results from multiple sources:
%s

Your job: extract every concrete fact from the above that could affect how this prediction market resolves. A trader will use this to decide whether to buy YES or NO.

Requirements:
- Prioritise NUMBERS: league position, points total, win/loss record, vote counts, percentages, poll numbers, prices, dates
- Each fact MUST include at least one specific number, date, or named outcome — if a snippet has no concrete data, skip it
- If sources contradict each other, note both with "⚡ CONFLICTING:" prefix
- Flag the single most resolution-critical fact with "🔑" prefix
- Include source name and date (e.g. "per ESPN, Mar 22")
- Ignore article navigation text, site chrome, and duplicate information

Format: bullet points starting with •. No intro, no conclusion, no commentary. Numbers and facts only.`, question, sb.String())

	req := claudeRequest{
		Model:     summarizerModel,
		MaxTokens: summarizerMaxTokens,
		System:    "You are a prediction market research analyst. Extract concrete, specific, actionable facts from search results. Be thorough but terse. Every fact you include should help a trader decide YES or NO.",
		Messages:  []claudeMsg{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ""
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", claudeBaseURL+claudeMessagesPath, bytes.NewReader(body))
	if err != nil {
		return ""
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", qg.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := qg.client.Do(httpReq)
	if err != nil {
		qg.logger.Warn("summarizer API call failed", slog.String("err", err.Error()))
		return ""
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		qg.logger.Warn("summarizer read response failed", slog.String("err", err.Error()))
		return ""
	}
	if resp.StatusCode != http.StatusOK {
		qg.logger.Warn("summarizer API error", slog.Int("status", resp.StatusCode), slog.String("body", string(respBytes)))
		return ""
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBytes, &claudeResp); err != nil || len(claudeResp.Content) == 0 {
		qg.logger.Warn("summarizer parse failed", slog.String("raw", string(respBytes)))
		return ""
	}

	qg.logger.Info("summarizer complete", slog.Int("facts_len", len(claudeResp.Content[0].Text)))
	return strings.TrimSpace(claudeResp.Content[0].Text)
}
