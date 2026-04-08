package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
	"github.com/badgateway/poly/internal/telegram"
	"github.com/badgateway/poly/internal/web"
)

func main() {
	// Load .env file (silent if missing — env vars may already be set)
	_ = godotenv.Load()

	// Parse command-line flags
	scanCmd := flag.NewFlagSet("scan", flag.ExitOnError)
	limit := scanCmd.Int("limit", 500, "number of markets to scan (default 500)")
	outputFile := scanCmd.String("output", "", "output file for results (default: scan-TIMESTAMP.json)")
	configFile := scanCmd.String("config", "config.yaml", "path to config file")

	testBookCmd := flag.NewFlagSet("test-book", flag.ExitOnError)
	tokenID := testBookCmd.String("token", "", "CLOB token ID to test")
	stakeUSD := testBookCmd.Float64("stake", 50.0, "stake size in USD (default $50)")

	telegramCmd := flag.NewFlagSet("telegram", flag.ExitOnError)
	telegramConfig := telegramCmd.String("config", "config.yaml", "path to config file")

	webCmd := flag.NewFlagSet("web", flag.ExitOnError)
	webConfig := webCmd.String("config", "config.yaml", "path to config file")
	webAddr := webCmd.String("addr", ":8080", "listen address")

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: poly <command> [options]\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  scan        Run 4-Layer Filter Pipeline\n")
		fmt.Fprintf(os.Stderr, "  web         Start Golden Rain web UI\n")
		fmt.Fprintf(os.Stderr, "  telegram    Start Telegram bot\n")
		fmt.Fprintf(os.Stderr, "  test-book   Test CLOB order book fetching\n")
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "scan":
		scanCmd.Parse(os.Args[2:])
		runScan(scanCmd, *limit, *outputFile, *configFile)
	case "web":
		webCmd.Parse(os.Args[2:])
		runWeb(*webConfig, *webAddr)
	case "telegram":
		telegramCmd.Parse(os.Args[2:])
		runTelegram(*telegramConfig)
	case "test-book":
		testBookCmd.Parse(os.Args[2:])
		runTestBook(*tokenID, *stakeUSD)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func runWeb(configFile, addr string) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	server := web.NewServer(cfg, logger)
	if err := server.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
		os.Exit(1)
	}
}

func runTelegram(configFile string) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Load config
	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Read credentials from environment
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		fmt.Fprintf(os.Stderr, "TELEGRAM_BOT_TOKEN not set in .env\n")
		os.Exit(1)
	}

	uidStr := os.Getenv("TELEGRAM_USER_ID")
	if uidStr == "" {
		fmt.Fprintf(os.Stderr, "TELEGRAM_USER_ID not set in .env\n")
		os.Exit(1)
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid TELEGRAM_USER_ID: %v\n", err)
		os.Exit(1)
	}

	// Create and start bot
	bot, err := telegram.New(token, uid, cfg, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start bot: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Golden Waterfall Bot started. Send /help to your bot.\n")
	if err := bot.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Bot error: %v\n", err)
		os.Exit(1)
	}
}

func runTestBook(tokenID string, stakeUSD float64) {
	if tokenID == "" {
		fmt.Fprintf(os.Stderr, "Error: --token is required\n")
		fmt.Fprintf(os.Stderr, "Usage: poly test-book --token <token_id> [--stake <amount>]\n")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	fmt.Fprintf(os.Stderr, "Golden Waterfall — CLOB Order Book Test\n")
	fmt.Fprintf(os.Stderr, "========================================\n")
	fmt.Fprintf(os.Stderr, "Token ID: %s\n", tokenID)
	fmt.Fprintf(os.Stderr, "Stake: $%.2f\n\n", stakeUSD)

	// Create CLOB client
	clobClient := polymarket.NewCLOBClient()

	// Fetch order book
	fmt.Fprintf(os.Stderr, "Fetching order book...\n")
	book, err := clobClient.GetOrderBook(tokenID)
	if err != nil {
		logger.Error("failed to fetch order book", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Parse book snapshot
	snapshot, err := polymarket.ParseBookSnapshot(book)
	if err != nil {
		logger.Error("failed to parse book snapshot", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Display book summary
	fmt.Fprintf(os.Stderr, "\n📊 Order Book Summary\n")
	fmt.Fprintf(os.Stderr, "  Best Bid:       $%.4f\n", snapshot.BestBid)
	fmt.Fprintf(os.Stderr, "  Best Ask:       $%.4f\n", snapshot.BestAsk)
	fmt.Fprintf(os.Stderr, "  Mid Price:      $%.4f\n", snapshot.MidPrice)
	fmt.Fprintf(os.Stderr, "  Spread:         $%.4f (%.2f%%)\n", snapshot.Spread, snapshot.SpreadPercent)
	fmt.Fprintf(os.Stderr, "  Bid Levels:     %d\n", len(snapshot.BidLevels))
	fmt.Fprintf(os.Stderr, "  Ask Levels:     %d\n", len(snapshot.AskLevels))

	// Calculate VWAP for BUY order
	fmt.Fprintf(os.Stderr, "\n📈 VWAP Analysis (BUY $%.2f)\n", stakeUSD)
	vwapResult, err := scanner.CalculateVWAP(snapshot, "BUY", stakeUSD)
	if err != nil {
		logger.Error("failed to calculate VWAP", slog.String("error", err.Error()))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "  VWAP:           $%.4f\n", vwapResult.VWAP)
	fmt.Fprintf(os.Stderr, "  Best Ask:       $%.4f\n", vwapResult.BestPrice)
	fmt.Fprintf(os.Stderr, "  Slippage:       $%.4f (+%.2f%%)\n", vwapResult.Slippage, vwapResult.SlippagePercent)
	fmt.Fprintf(os.Stderr, "  Shares Filled:  %.2f\n", vwapResult.SharesFilled)
	fmt.Fprintf(os.Stderr, "  Levels Crossed: %d\n", vwapResult.LevelsCrossed)
	fmt.Fprintf(os.Stderr, "  Sufficient:     %v\n", vwapResult.Sufficient)
	fmt.Fprintf(os.Stderr, "  Quality:        %s\n", vwapResult.FillQuality())

	// Calculate true depth
	fmt.Fprintf(os.Stderr, "\n💧 True Depth Analysis (±2%%)\n")
	depth, err := scanner.CalculateTrueDepthDefault(snapshot)
	if err != nil {
		logger.Error("failed to calculate true depth", slog.String("error", err.Error()))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "  Mid Price:      $%.4f\n", depth.MidPrice)
	fmt.Fprintf(os.Stderr, "  Range:          $%.4f - $%.4f\n", depth.LowerBound, depth.UpperBound)
	fmt.Fprintf(os.Stderr, "  Bid Depth:      $%.2f (%d levels)\n", depth.BidDepthUSD, depth.BidLevelsCount)
	fmt.Fprintf(os.Stderr, "  Ask Depth:      $%.2f (%d levels)\n", depth.AskDepthUSD, depth.AskLevelsCount)
	fmt.Fprintf(os.Stderr, "  Total Depth:    $%.2f\n", depth.TotalDepthUSD)
	fmt.Fprintf(os.Stderr, "  Imbalance:      %+.1f%% (%s)\n", depth.DepthImbalance*100, depth.ImbalanceDirection())

	fmt.Fprintf(os.Stderr, "\n✅ Test complete\n")
}

func runScan(scanCmd *flag.FlagSet, limit int, outputFile, configFile string) {

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup structured logging (to stderr)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	fmt.Fprintf(os.Stderr, "Golden Waterfall Trading System\n")
	fmt.Fprintf(os.Stderr, "Phase 1 - Milestone 1.2b: 4-Layer Filter Pipeline (L1→L2→L3→L4)\n")
	fmt.Fprintf(os.Stderr, "===================================================================\n")
	fmt.Fprintf(os.Stderr, "Config: %s\n", configFile)
	fmt.Fprintf(os.Stderr, "Golden Zone: $%.2f - $%.2f\n", cfg.GoldenZone.Min, cfg.GoldenZone.Max)
	fmt.Fprintf(os.Stderr, "Volume Min (24h): $%.0f\n", cfg.Liveness.MinVolume24h)
	fmt.Fprintf(os.Stderr, "Liquidity Min: $%.0f\n", cfg.Liveness.MinLiquidity)
	fmt.Fprintf(os.Stderr, "Horizon: %d-%d days\n", cfg.Quality.HorizonMinDays, cfg.Quality.HorizonMaxDays)
	fmt.Fprintf(os.Stderr, "Tier A Min: $%.0f, Tier B Min: $%.0f\n\n", cfg.LiquidityTiers.TierAMin, cfg.LiquidityTiers.TierBMin)

	// Create Gamma API client
	gammaClient := polymarket.NewGammaClient(logger)

	// Fetch markets with limit
	fmt.Fprintf(os.Stderr, "Fetching up to %d markets from Gamma API...\n", limit)
	markets, err := gammaClient.FetchMarketsLimit(limit)
	if err != nil {
		logger.Error("failed to fetch markets", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if len(markets) == 0 {
		logger.Warn("no active markets found")
		os.Exit(0)
	}

	logger.Info("markets fetched", slog.Int("count", len(markets)))

	// Create CLOB client for L4 order book analysis
	clobClient := polymarket.NewCLOBClient()

	// Run 4-layer pipeline: L1→L2→L3→L4
	fmt.Fprintf(os.Stderr, "\nRunning 4-layer filter pipeline...\n\n")
	pipelineResult := scanner.RunPipeline(markets, cfg, clobClient, logger)

	// Print layered output
	fmt.Fprintf(os.Stderr, "\n========== FILTER PIPELINE RESULTS ==========\n")
	fmt.Fprintf(os.Stderr, "Total markets scanned: %d\n\n", pipelineResult.TotalScanned)

	// L1 results
	fmt.Fprintf(os.Stderr, "L1 (Category Gate):\n")
	fmt.Fprintf(os.Stderr, "  Passed:   %d\n", pipelineResult.L1Result.Passed)
	fmt.Fprintf(os.Stderr, "  Rejected: %d\n", pipelineResult.L1Result.Rejected)
	if len(pipelineResult.L1Result.RejectCounts) > 0 {
		for reason, count := range pipelineResult.L1Result.RejectCounts {
			fmt.Fprintf(os.Stderr, "    - %s: %d\n", reason, count)
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	// L2 results
	fmt.Fprintf(os.Stderr, "L2 (Liveness Check):\n")
	fmt.Fprintf(os.Stderr, "  Passed:   %d\n", pipelineResult.L2Result.Passed)
	fmt.Fprintf(os.Stderr, "  Rejected: %d\n", pipelineResult.L2Result.Rejected)
	if len(pipelineResult.L2Result.RejectCounts) > 0 {
		for reason, count := range pipelineResult.L2Result.RejectCounts {
			fmt.Fprintf(os.Stderr, "    - %s: %d\n", reason, count)
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	// L3 results
	fmt.Fprintf(os.Stderr, "L3 (Quality Gate):\n")
	fmt.Fprintf(os.Stderr, "  Passed:   %d\n", pipelineResult.L3Result.Passed)
	fmt.Fprintf(os.Stderr, "  Rejected: %d\n", pipelineResult.L3Result.Rejected)
	if len(pipelineResult.L3Result.RejectCounts) > 0 {
		for reason, count := range pipelineResult.L3Result.RejectCounts {
			fmt.Fprintf(os.Stderr, "    - %s: %d\n", reason, count)
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	// L3 survivors (before L4)
	fmt.Fprintf(os.Stderr, "L3 Survivors: %d\n\n", len(pipelineResult.Passed))

	// L4 results
	fmt.Fprintf(os.Stderr, "L4 (Distribution Engine):\n")
	fmt.Fprintf(os.Stderr, "  Alpha:  %d  (ready for approval)\n", pipelineResult.L4Result.Passed)
	fmt.Fprintf(os.Stderr, "  Shadow: %d  (monitor only)\n", pipelineResult.L4Result.Rejected)
	if len(pipelineResult.L4Result.RejectCounts) > 0 {
		for reason, count := range pipelineResult.L4Result.RejectCounts {
			fmt.Fprintf(os.Stderr, "    - %s: %d\n", reason, count)
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Signal summary
	fmt.Fprintf(os.Stderr, "=== SIGNALS ===\n")
	for _, sig := range pipelineResult.Signals {
		route := "SHADOW"
		if sig.IsAlpha() {
			route = "ALPHA "
		}
		price := sig.YesPrice
		if sig.TargetSide == "NO" {
			price = sig.NoPrice
		}
		fmt.Fprintf(os.Stderr, "  [%s] score=%.1f θ=%.2f %s | %s → buy %s @ $%.3f (VWAP $%.4f, D/V=%.3f, depth $%.0f, spread %.1f%%)\n",
			route,
			sig.Score,
			sig.ThetaMultiplier,
			string(sig.Activity),
			truncate(sig.Market.Question, 45),
			sig.TargetSide,
			price,
			sig.FilteredMarket.VWAP,
			sig.DVRatio,
			sig.FilteredMarket.TrueDepthUSD,
			sig.FilteredMarket.SpreadPercent,
		)
		if !sig.IsAlpha() {
			fmt.Fprintf(os.Stderr, "         → %v\n", sig.ShadowReasons)
		}
	}

	// Generate output filename if not provided
	if outputFile == "" {
		timestamp := time.Now().Format("2006-01-02-150405")
		outputFile = filepath.Join("scan-results", fmt.Sprintf("scan-%s.json", timestamp))
		os.MkdirAll("scan-results", 0755)
	}

	// Create output directory if it doesn't exist
	outDir := filepath.Dir(outputFile)
	os.MkdirAll(outDir, 0755)

	// Write signals to file (all L3 survivors with Alpha/Shadow routing)
	output, err := json.MarshalIndent(pipelineResult.Signals, "", "  ")
	if err != nil {
		logger.Error("failed to marshal filtered markets", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		logger.Error("failed to write output file", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Write markdown report
	mdFile := filepath.Join(filepath.Dir(outputFile), strings.TrimSuffix(filepath.Base(outputFile), ".json")+".md")
	if err := writeMarkdownReport(mdFile, pipelineResult, cfg); err != nil {
		logger.Error("failed to write markdown report", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Write rejection log
	if len(pipelineResult.AllRejects) > 0 {
		rejectFile := filepath.Join(filepath.Dir(outputFile), strings.TrimSuffix(filepath.Base(outputFile), ".json")+"-rejects.json")
		rejectData, err := json.MarshalIndent(pipelineResult.AllRejects, "", "  ")
		if err != nil {
			logger.Error("failed to marshal rejects", slog.String("error", err.Error()))
		} else {
			if err := os.WriteFile(rejectFile, rejectData, 0644); err != nil {
				logger.Error("failed to write rejects file", slog.String("error", err.Error()))
			} else {
				fmt.Fprintf(os.Stderr, "\n✅ Results written to:\n")
				fmt.Fprintf(os.Stderr, "   JSON:     %s\n", outputFile)
				fmt.Fprintf(os.Stderr, "   Markdown: %s\n", mdFile)
				fmt.Fprintf(os.Stderr, "   Rejects:  %s (%d markets)\n", rejectFile, len(pipelineResult.AllRejects))
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "\n✅ Results written to:\n")
		fmt.Fprintf(os.Stderr, "   JSON:     %s\n", outputFile)
		fmt.Fprintf(os.Stderr, "   Markdown: %s\n", mdFile)
	}

	logger.Info("scan complete",
		slog.Int("total_scanned", pipelineResult.TotalScanned),
		slog.Int("l1_passed", pipelineResult.L1Result.Passed),
		slog.Int("l2_passed", pipelineResult.L2Result.Passed),
		slog.Int("l3_passed", pipelineResult.L3Result.Passed),
		slog.Int("l4_alpha", pipelineResult.L4Result.Passed),
		slog.Int("l4_shadow", pipelineResult.L4Result.Rejected),
		slog.Int("hard_rejected", len(pipelineResult.AllRejects)),
		slog.String("output_file", outputFile))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// writeMarkdownReport generates a markdown file with pipeline results and signal table
func writeMarkdownReport(filename string, result scanner.PipelineResult, cfg *config.Config) error {
	var sb strings.Builder

	alphaCount := result.L4Result.Passed
	shadowCount := result.L4Result.Rejected

	sb.WriteString("# Golden Zone Market Scan — 4-Layer Pipeline\n\n")
	sb.WriteString(fmt.Sprintf("**Scan Date:** %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**Total Markets Scanned:** %d\n", result.TotalScanned))
	sb.WriteString(fmt.Sprintf("**L3 Survivors:** %d | **Alpha:** %d | **Shadow:** %d\n\n",
		len(result.Passed), alphaCount, shadowCount))

	// Filter configuration
	sb.WriteString("## Filter Configuration\n\n")
	sb.WriteString(fmt.Sprintf("- **Golden Zone:** $%.2f - $%.2f\n", cfg.GoldenZone.Min, cfg.GoldenZone.Max))
	sb.WriteString(fmt.Sprintf("- **Min Liquidity:** $%.0f\n", cfg.Liveness.MinLiquidity))
	sb.WriteString(fmt.Sprintf("- **Min Volume (24h):** $%.0f\n", cfg.Liveness.MinVolume24h))
	sb.WriteString(fmt.Sprintf("- **Horizon:** %d-%d days to resolution\n", cfg.Quality.HorizonMinDays, cfg.Quality.HorizonMaxDays))
	sb.WriteString(fmt.Sprintf("- **Max Stake:** $%.0f (%.0f%% of $%.0f)\n",
		cfg.Distribution.DefaultBalance*cfg.Distribution.DefaultStakePct,
		cfg.Distribution.DefaultStakePct*100, cfg.Distribution.DefaultBalance))
	sb.WriteString(fmt.Sprintf("- **Min True Depth:** $%.0f | **Max Spread:** %.1f%%\n\n",
		cfg.Distribution.MinTrueDepthUSD, cfg.Distribution.MaxSpreadPct))

	// Pipeline results
	sb.WriteString("## Pipeline Results\n\n")
	sb.WriteString(fmt.Sprintf("- **L1 (Category Gate):** %d passed, %d rejected\n", result.L1Result.Passed, result.L1Result.Rejected))
	sb.WriteString(fmt.Sprintf("- **L2 (Liveness Check):** %d passed, %d rejected\n", result.L2Result.Passed, result.L2Result.Rejected))
	sb.WriteString(fmt.Sprintf("- **L3 (Quality Gate):** %d passed, %d rejected\n", result.L3Result.Passed, result.L3Result.Rejected))
	sb.WriteString(fmt.Sprintf("- **L4 (Distribution Engine):** %d alpha, %d shadow\n\n", alphaCount, shadowCount))

	// Rejection counts (L1-L3 hard rejects)
	if len(result.L1Result.RejectCounts) > 0 || len(result.L2Result.RejectCounts) > 0 || len(result.L3Result.RejectCounts) > 0 {
		sb.WriteString("## Hard Rejection Breakdown (L1-L3)\n\n")
		sb.WriteString("| Layer | Reason | Count |\n")
		sb.WriteString("|-------|--------|-------|\n")
		for reason, count := range result.L1Result.RejectCounts {
			sb.WriteString(fmt.Sprintf("| L1 | %s | %d |\n", reason, count))
		}
		for reason, count := range result.L2Result.RejectCounts {
			sb.WriteString(fmt.Sprintf("| L2 | %s | %d |\n", reason, count))
		}
		for reason, count := range result.L3Result.RejectCounts {
			sb.WriteString(fmt.Sprintf("| L3 | %s | %d |\n", reason, count))
		}
		sb.WriteString("\n")
	}

	// Alpha signals table
	alphaSignals := make([]scanner.Signal, 0)
	shadowSignals := make([]scanner.Signal, 0)
	for _, sig := range result.Signals {
		if sig.IsAlpha() {
			alphaSignals = append(alphaSignals, sig)
		} else {
			shadowSignals = append(shadowSignals, sig)
		}
	}

	if len(alphaSignals) > 0 {
		sb.WriteString("## Alpha Signals (Ready for Approval)\n\n")
		sb.WriteString("| # | Score | Market | Side | VWAP | Depth | D/V | Spread | θ | Activity | Tier | Days | Link |\n")
		sb.WriteString("|---|-------|--------|------|------|-------|-----|--------|---|----------|------|------|------|\n")
		for i, sig := range alphaSignals {
			q := sig.Market.Question
			if len(q) > 50 {
				q = q[:47] + "..."
			}
			sb.WriteString(fmt.Sprintf("| %d | %.1f | %s | %s | $%.4f | $%.0f | %.3f | %.1f%% | %.2f | %s | %s | %d | [View](%s) |\n",
				i+1,
				sig.Score,
				q,
				sig.TargetSide,
				sig.FilteredMarket.VWAP,
				sig.FilteredMarket.TrueDepthUSD,
				sig.DVRatio,
				sig.FilteredMarket.SpreadPercent,
				sig.ThetaMultiplier,
				string(sig.Activity),
				string(sig.Tier),
				sig.DaysToResolve,
				sig.URL,
			))
		}
		sb.WriteString("\n")
	}

	if len(shadowSignals) > 0 {
		sb.WriteString("## Shadow Signals (Monitor Only)\n\n")
		sb.WriteString("| # | Market | Side | VWAP | Depth | D/V | Spread | θ | Activity | Tier | Days | Shadow Reason | Link |\n")
		sb.WriteString("|---|--------|------|------|-------|-----|--------|---|----------|------|------|---------------|------|\n")
		for i, sig := range shadowSignals {
			q := sig.Market.Question
			if len(q) > 40 {
				q = q[:37] + "..."
			}
			reasons := strings.Join(sig.ShadowReasons, ", ")
			sb.WriteString(fmt.Sprintf("| %d | %s | %s | $%.4f | $%.0f | %.3f | %.1f%% | %.2f | %s | %s | %d | %s | [View](%s) |\n",
				i+1, q,
				sig.TargetSide,
				sig.FilteredMarket.VWAP,
				sig.FilteredMarket.TrueDepthUSD,
				sig.DVRatio,
				sig.FilteredMarket.SpreadPercent,
				sig.ThetaMultiplier,
				string(sig.Activity),
				string(sig.Tier),
				sig.DaysToResolve,
				reasons,
				sig.URL,
			))
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(filename, []byte(sb.String()), 0644)
}
