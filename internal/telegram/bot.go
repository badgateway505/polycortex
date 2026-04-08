package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

const (
	promptTemplatePath  = "templates/pillarlab_prompt.md"
	auditorTemplatePath = "templates/claude_auditor_prompt.md"
)

// Bot wraps the Telegram bot and handles commands
type Bot struct {
	api           *telego.Bot
	cfg           *config.Config
	logger        *slog.Logger
	mu            sync.RWMutex
	lastResult    *scanner.PipelineResult
	tavilyClient  *analysis.TavilyClient
	// Tavily results cached per market ID from last search
	tavilyCache   map[string]*analysis.SearchContext
}

// New creates a new Telegram bot
func New(token string, _ int64, cfg *config.Config, logger *slog.Logger) (*Bot, error) {
	api, err := telego.NewBot(token)
	if err != nil {
		return nil, fmt.Errorf("connect to Telegram: %w", err)
	}

	me, err := api.GetMe(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get bot info: %w", err)
	}

	logger.Info("Telegram bot connected",
		slog.String("username", me.Username))

	// Initialize Tavily client if API key is set
	var tavilyClient *analysis.TavilyClient
	if key := os.Getenv("TAVILY_API_KEY"); key != "" {
		tavilyClient = analysis.NewTavilyClient(key, logger)
		logger.Info("Tavily client initialized")
	} else {
		logger.Warn("TAVILY_API_KEY not set — /tavily command will be unavailable")
	}

	return &Bot{
		api:          api,
		cfg:          cfg,
		logger:       logger,
		tavilyClient: tavilyClient,
		tavilyCache:  make(map[string]*analysis.SearchContext),
	}, nil
}

// Run starts the bot's update loop (blocks until stopped)
func (b *Bot) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates, err := b.api.UpdatesViaLongPolling(ctx, nil)
	if err != nil {
		return fmt.Errorf("start polling: %w", err)
	}

	b.logger.Info("Bot listening for commands")

	for update := range updates {
		if update.Message == nil {
			continue
		}

		msg := update.Message
		chatID := msg.Chat.ID
		threadID := msg.MessageThreadID // 0 for General / non-forum chats
		text := strings.TrimSpace(msg.Text)

		b.logger.Info("Received command",
			slog.String("text", text),
			slog.Int64("chat_id", chatID),
			slog.Int("thread_id", threadID))

		switch {
		case strings.HasPrefix(text, "/scan"):
			go b.handleScan(chatID, threadID, text)
		case strings.HasPrefix(text, "/shadow"):
			go b.handleShadow(chatID, threadID)
		case strings.HasPrefix(text, "/prompt"):
			go b.handlePrompt(chatID, threadID, text)
		case strings.HasPrefix(text, "/research"):
			go b.handleResearch(chatID, threadID, text)
		case strings.HasPrefix(text, "/tavily"):
			go b.handleTavily(chatID, threadID, text)
		case strings.HasPrefix(text, "/auditor"):
			go b.handleAuditor(chatID, threadID, text)
		case strings.HasPrefix(text, "/import"):
			go b.handleImport(chatID, threadID, text)
		case strings.HasPrefix(text, "/status"):
			b.send(chatID, threadID, b.handleStatus(), false)
		case strings.HasPrefix(text, "/help"):
			b.send(chatID, threadID, helpText(), false)
		default:
			b.send(chatID, threadID, "Unknown command. Use /help for available commands.", false)
		}
	}

	return nil
}

// handleScan runs the full L1→L4 pipeline and sends formatted results
func (b *Bot) handleScan(chatID int64, threadID int, text string) {
	// Parse limit: /scan 100  or  /scan --limit 100
	const maxScanLimit = 40000
	limit := 500
	parts := strings.Fields(text)
	for i, p := range parts {
		if p == "--limit" && i+1 < len(parts) {
			if n, err := strconv.Atoi(parts[i+1]); err == nil && n > 0 {
				limit = n
			}
		}
	}
	if len(parts) == 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxScanLimit {
		limit = maxScanLimit
	}

	b.send(chatID, threadID, fmt.Sprintf("🔍 Scanning %d markets... (this takes ~30s)", limit), false)

	start := time.Now()

	gammaClient := polymarket.NewGammaClient(b.logger)
	clobClient := polymarket.NewCLOBClient()

	markets, err := gammaClient.FetchMarketsLimit(limit)
	if err != nil {
		b.send(chatID, threadID, fmt.Sprintf("❌ Failed to fetch markets: %v", err), false)
		return
	}
	if len(markets) == 0 {
		b.send(chatID, threadID, "⚠️ No markets returned from Gamma API", false)
		return
	}

	result := scanner.RunPipeline(markets, b.cfg, clobClient, b.logger)
	duration := time.Since(start)

	b.mu.Lock()
	b.lastResult = &result
	b.mu.Unlock()

	for _, msg := range FormatScanResults(result, limit, duration) {
		b.send(chatID, threadID, msg, true)
	}
}

// handleShadow sends the shadow signals from the most recent scan
func (b *Bot) handleShadow(chatID int64, threadID int) {
	b.mu.RLock()
	result := b.lastResult
	b.mu.RUnlock()

	if result == nil {
		b.send(chatID, threadID, "No scan results yet. Run /scan first.", false)
		return
	}

	for _, msg := range FormatShadowResults(*result) {
		b.send(chatID, threadID, msg, true)
	}
}

// handlePrompt generates a PillarLab prompt.
// /prompt → all Alpha signals (batch mode)
// /prompt N → single Alpha signal #N
func (b *Bot) handlePrompt(chatID int64, threadID int, text string) {
	b.mu.RLock()
	result := b.lastResult
	b.mu.RUnlock()

	if result == nil {
		b.send(chatID, threadID, "No scan results yet. Run /scan first.", false)
		return
	}

	var alphas []scanner.Signal
	for _, sig := range result.Signals {
		if sig.IsAlpha() {
			alphas = append(alphas, sig)
		}
	}
	if len(alphas) == 0 {
		b.send(chatID, threadID, "No Alpha signals in last scan. Nothing to send to PillarLab.", false)
		return
	}

	// Load template
	tmpl, err := os.ReadFile(promptTemplatePath)
	if err != nil {
		b.send(chatID, threadID, fmt.Sprintf("Failed to load prompt template: %v", err), false)
		return
	}

	// Check for single signal mode: /prompt N
	parts := strings.Fields(text)
	var prompt string
	var label string

	if len(parts) >= 2 {
		arg := parts[1]
		if strings.HasPrefix(arg, "@") && len(parts) >= 3 {
			arg = parts[2]
		}
		if n, err := strconv.Atoi(arg); err == nil {
			if n >= 1 && n <= len(alphas) {
				prompt = analysis.GenerateSinglePrompt(string(tmpl), alphas[n-1])
				label = fmt.Sprintf("Copy into PillarLab (Alpha #%d/%d).\nPaste the JSON response with /import <JSON>", n, len(alphas))
			} else {
				b.send(chatID, threadID, fmt.Sprintf("Only %d Alpha signals. Use /prompt 1-%d", len(alphas), len(alphas)), false)
				return
			}
		}
	}

	if prompt == "" {
		// Batch mode: all alphas
		prompt = analysis.GeneratePrompt(string(tmpl), result.Signals)
		label = fmt.Sprintf("Copy into PillarLab (%d markets).\nPaste the JSON response with /import <JSON>", len(alphas))
	}

	b.sendLongText(chatID, threadID, prompt)
	b.send(chatID, threadID, label, false)
}

// handleResearch generates per-market deep research prompts for Perplexity
func (b *Bot) handleResearch(chatID int64, threadID int, text string) {
	b.mu.RLock()
	result := b.lastResult
	b.mu.RUnlock()

	if result == nil {
		b.send(chatID, threadID, "No scan results yet. Run /scan first.", false)
		return
	}

	// Collect alpha signals
	var alphas []scanner.Signal
	for _, sig := range result.Signals {
		if sig.IsAlpha() {
			alphas = append(alphas, sig)
		}
	}
	if len(alphas) == 0 {
		b.send(chatID, threadID, "No Alpha signals in last scan.", false)
		return
	}

	// Parse which market to show: /research, /research next, /research 3
	parts := strings.Fields(text)
	idx := 0 // default: first market

	if len(parts) >= 2 {
		arg := parts[1]
		// Handle @botname suffix on the command itself
		if strings.HasPrefix(arg, "@") && len(parts) >= 3 {
			arg = parts[2]
		}
		if n, err := strconv.Atoi(arg); err == nil && n >= 1 && n <= len(alphas) {
			idx = n - 1
		} else if arg == "all" {
			// Send a list of all alpha signals with their categories
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("📋 %d Alpha signals available for research:\n\n", len(alphas)))
			for i, sig := range alphas {
				cat := analysis.DetectCategory(sig.Market.Question)
				sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, cat.Label(), truncate(sig.Market.Question, 60)))
			}
			sb.WriteString("\nUse /research N to get the prompt for signal #N")
			b.send(chatID, threadID, sb.String(), false)
			return
		}
	}

	if idx >= len(alphas) {
		b.send(chatID, threadID, fmt.Sprintf("Only %d Alpha signals. Use /research 1-%d", len(alphas), len(alphas)), false)
		return
	}

	sig := alphas[idx]
	prompt := analysis.ResearchPrompt(sig)

	// Send header
	cat := analysis.DetectCategory(sig.Market.Question)
	b.send(chatID, threadID, fmt.Sprintf("🔬 Research prompt for Alpha #%d/%d [%s]:\nCopy and paste into Perplexity Deep Research 👇", idx+1, len(alphas), cat.Label()), false)

	// Split into chunks if needed (Telegram 4096 char limit)
	const maxLen = 4000
	if len(prompt) <= maxLen {
		b.send(chatID, threadID, prompt, false)
	} else {
		lines := strings.Split(prompt, "\n")
		var chunk strings.Builder
		for _, line := range lines {
			if chunk.Len()+len(line)+1 > maxLen {
				b.send(chatID, threadID, chunk.String(), false)
				chunk.Reset()
			}
			chunk.WriteString(line)
			chunk.WriteString("\n")
		}
		if chunk.Len() > 0 {
			b.send(chatID, threadID, chunk.String(), false)
		}
	}

	if idx+1 < len(alphas) {
		b.send(chatID, threadID, fmt.Sprintf("Use /research %d for the next signal", idx+2), false)
	}
}

// handleTavily fetches real-time news for a specific Alpha signal.
// /tavily N → fetch Tavily news for Alpha #N, cache the result
func (b *Bot) handleTavily(chatID int64, threadID int, text string) {
	if b.tavilyClient == nil {
		b.send(chatID, threadID, "Tavily not configured. Set TAVILY_API_KEY in .env", false)
		return
	}

	alphas := b.getAlphas()
	if alphas == nil {
		return
	}

	idx, ok := b.parseSignalIndex(chatID, threadID, text, len(alphas), "tavily")
	if !ok {
		return
	}

	sig := alphas[idx]
	cat := analysis.DetectCategory(sig.Market.Question)
	b.send(chatID, threadID, fmt.Sprintf("🔍 Searching Tavily for Alpha #%d [%s]...", idx+1, cat.Label()), false)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sc, err := b.tavilyClient.SearchForSignal(ctx, sig)
	if err != nil {
		b.send(chatID, threadID, fmt.Sprintf("❌ Tavily search failed: %v", err), false)
		return
	}

	// Cache result
	b.mu.Lock()
	b.tavilyCache[sig.Market.ID] = sc
	b.mu.Unlock()

	// Format and send
	header := fmt.Sprintf("📰 <b>Tavily News — Alpha #%d</b>\n<b>%s</b>\nQuery: %s\n%d results found\n",
		idx+1,
		sig.Market.Question,
		sc.Query,
		len(sc.Results),
	)
	b.send(chatID, threadID, header, true)

	// Send answer summary
	if sc.Answer != "" {
		b.send(chatID, threadID, "💡 Summary: "+sc.Answer, false)
	}

	// Send individual results
	for i, r := range sc.Results {
		msg := fmt.Sprintf("[%d] %s\n%s", i+1, r.Title, r.URL)
		if r.PublishedDate != "" {
			msg += "\nDate: " + r.PublishedDate
		}
		if r.Content != "" {
			msg += "\n" + truncate(r.Content, 500)
		}
		b.send(chatID, threadID, msg, false)
	}

	b.send(chatID, threadID, fmt.Sprintf("✅ Cached for /auditor %d. Next: /research %d (Perplexity) → /prompt %d (PillarLab) → /auditor %d (Claude)", idx+1, idx+1, idx+1, idx+1), false)
}

// handleAuditor generates a Claude Auditor prompt for a specific Alpha signal.
// Pre-fills Tavily context if available from /tavily cache.
// /auditor N → Claude Auditor prompt for Alpha #N
func (b *Bot) handleAuditor(chatID int64, threadID int, text string) {
	alphas := b.getAlphas()
	if alphas == nil {
		b.send(chatID, threadID, "No scan results yet. Run /scan first.", false)
		return
	}

	idx, ok := b.parseSignalIndex(chatID, threadID, text, len(alphas), "auditor")
	if !ok {
		return
	}

	sig := alphas[idx]

	// Load auditor template
	tmpl, err := os.ReadFile(auditorTemplatePath)
	if err != nil {
		b.send(chatID, threadID, fmt.Sprintf("Failed to load auditor template: %v", err), false)
		return
	}

	// Get cached Tavily context if available
	b.mu.RLock()
	sc := b.tavilyCache[sig.Market.ID]
	b.mu.RUnlock()

	var tavilyContext string
	if sc != nil {
		tavilyContext = sc.FormatForClaude()
	}

	prompt := analysis.GenerateAuditorPrompt(string(tmpl), sig, tavilyContext, "", "")

	cat := analysis.DetectCategory(sig.Market.Question)
	hasTavily := "❌ no"
	if sc != nil {
		hasTavily = "✅ yes"
	}
	b.send(chatID, threadID, fmt.Sprintf("🧠 Claude Auditor prompt for Alpha #%d/%d [%s]\nTavily context: %s\nCopy and paste into Claude Opus 👇",
		idx+1, len(alphas), cat.Label(), hasTavily), false)

	b.sendLongText(chatID, threadID, prompt)

	b.send(chatID, threadID, "Replace [PASTE PERPLEXITY...] and [PASTE PILLARLAB...] with the actual outputs before sending to Claude.", false)
}

// getAlphas returns alpha signals from the last scan, or nil if no scan exists.
func (b *Bot) getAlphas() []scanner.Signal {
	b.mu.RLock()
	result := b.lastResult
	b.mu.RUnlock()

	if result == nil {
		return nil
	}

	var alphas []scanner.Signal
	for _, sig := range result.Signals {
		if sig.IsAlpha() {
			alphas = append(alphas, sig)
		}
	}
	return alphas
}

// parseSignalIndex extracts the signal number from a command like "/cmd N".
// Returns 0-based index and true on success, or sends an error message and returns false.
func (b *Bot) parseSignalIndex(chatID int64, threadID int, text string, maxSignals int, cmdName string) (int, bool) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		b.send(chatID, threadID, fmt.Sprintf("Usage: /%s N (e.g., /%s 1)", cmdName, cmdName), false)
		return 0, false
	}

	arg := parts[1]
	if strings.HasPrefix(arg, "@") && len(parts) >= 3 {
		arg = parts[2]
	}

	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > maxSignals {
		b.send(chatID, threadID, fmt.Sprintf("Invalid signal number. Use /%s 1-%d", cmdName, maxSignals), false)
		return 0, false
	}

	return n - 1, true
}

// sendLongText sends text that may exceed Telegram's 4096 char limit,
// splitting at line boundaries.
func (b *Bot) sendLongText(chatID int64, threadID int, text string) {
	const maxLen = 4000
	if len(text) <= maxLen {
		b.send(chatID, threadID, text, false)
		return
	}

	lines := strings.Split(text, "\n")
	var chunk strings.Builder
	for _, line := range lines {
		if chunk.Len()+len(line)+1 > maxLen {
			b.send(chatID, threadID, chunk.String(), false)
			chunk.Reset()
		}
		chunk.WriteString(line)
		chunk.WriteString("\n")
	}
	if chunk.Len() > 0 {
		b.send(chatID, threadID, chunk.String(), false)
	}
}

// handleImport parses PillarLab JSON output and matches it to the last scan's signals
func (b *Bot) handleImport(chatID int64, threadID int, text string) {
	b.mu.RLock()
	result := b.lastResult
	b.mu.RUnlock()

	if result == nil {
		b.send(chatID, threadID, "No scan results yet. Run /scan first, then /prompt, then /import.", false)
		return
	}

	// Strip the /import command prefix (handle @botname suffix too)
	jsonStr := text
	if idx := strings.Index(jsonStr, " "); idx != -1 {
		jsonStr = strings.TrimSpace(jsonStr[idx+1:])
	} else {
		b.send(chatID, threadID, "Usage: /import <JSON from PillarLab>\n\nPaste the JSON array that PillarLab returned.", false)
		return
	}

	if jsonStr == "" {
		b.send(chatID, threadID, "Usage: /import <JSON from PillarLab>\n\nPaste the JSON array that PillarLab returned.", false)
		return
	}

	// Parse PillarLab output
	predictions, err := analysis.ParsePillarLabOutput(jsonStr)
	if err != nil {
		b.send(chatID, threadID, fmt.Sprintf("Failed to parse PillarLab output: %v", err), false)
		return
	}

	// Match predictions to signals
	importResult := analysis.MatchPredictions(predictions, result.Signals)

	// Format and send results
	for _, msg := range FormatImportResults(importResult) {
		b.send(chatID, threadID, msg, true)
	}
}

// handleStatus returns a placeholder status message
func (b *Bot) handleStatus() string {
	return `📊 System Status

🤖 Bot: Online
📡 Scanner: Ready
💼 Positions: None (paper trading not yet active)
💰 Balance: $0 (live trading not yet active)

Use /scan to run a market scan.`
}

// send sends a message to a chat, in the correct topic thread if threadID != 0.
func (b *Bot) send(chatID int64, threadID int, text string, html bool) {
	params := &telego.SendMessageParams{
		ChatID: tu.ID(chatID),
		Text:   text,
	}
	if threadID != 0 {
		params.MessageThreadID = threadID
	}
	if html {
		params.ParseMode = telego.ModeHTML
		params.LinkPreviewOptions = &telego.LinkPreviewOptions{IsDisabled: true}
	}
	if _, err := b.api.SendMessage(context.Background(), params); err != nil {
		b.logger.Error("Failed to send message",
			slog.String("error", err.Error()),
			slog.String("text_preview", truncate(text, 100)))
	}
}

func helpText() string {
	return `🤖 Polycortex

Scan:
/scan 5000 — Run market scan
/shadow — Show shadow signals

Per-Signal Pipeline:
/tavily N — Fetch real-time news for Alpha #N
/research N — Perplexity deep research prompt for Alpha #N
/prompt N — PillarLab prompt for Alpha #N
/auditor N — Claude Auditor prompt for Alpha #N (with Tavily context)

Batch:
/prompt — PillarLab prompt for ALL Alphas
/research all — List Alpha signals with categories
/import <JSON> — Import predictions (PillarLab or Perplexity)

System:
/status — Show system status
/help — Show this message

Per-Signal Workflow:
1. /scan 5000 → find Alpha signals
2. /tavily 1 → auto-fetch news for #1
3. /research 1 → copy prompt to Perplexity
4. /prompt 1 → copy prompt to PillarLab
5. /auditor 1 → copy prompt to Claude Opus (Tavily pre-filled)
6. Compare all 3 outputs → /import <JSON>`
}
