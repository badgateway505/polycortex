package telegram

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/scanner"
)

// FormatScanResults converts a pipeline result into a slice of Telegram HTML messages.
// Returns multiple messages because Telegram has a 4096 char limit per message.
func FormatScanResults(result scanner.PipelineResult, limit int, duration time.Duration) []string {
	var messages []string

	// --- Header / Summary ---
	alpha := result.L4Result.Passed
	shadow := result.L4Result.Rejected

	var status string
	if alpha > 0 {
		status = "🟢 <b>Alpha signals found!</b>"
	} else {
		status = "🟡 <b>No Alpha signals today</b>"
	}

	header := fmt.Sprintf(
		`%s

📊 <b>Scan Summary</b>
├ Scanned: %d markets in %.1fs
├ L2 (Liveness): %d passed
├ L3 (Quality): %d passed
├ L4 Alpha: <b>%d</b> ✅
└ L4 Shadow: %d 👁

⚙️ Golden Zone $0.20–$0.40 | Max stake $50 | Limit %d`,
		status,
		result.TotalScanned,
		duration.Seconds(),
		result.L2Result.Passed,
		result.L3Result.Passed,
		alpha,
		shadow,
		limit,
	)
	messages = append(messages, header)

	// --- Alpha signals ---
	alphaSignals := make([]scanner.Signal, 0)
	shadowSignals := make([]scanner.Signal, 0)
	for _, s := range result.Signals {
		if s.IsAlpha() {
			alphaSignals = append(alphaSignals, s)
		} else {
			shadowSignals = append(shadowSignals, s)
		}
	}

	for i, sig := range alphaSignals {
		messages = append(messages, formatAlphaSignal(i+1, sig))
	}

	// --- Shadow signals (one combined message) ---
	if len(shadowSignals) > 0 {
		messages = append(messages, formatShadowBlock(shadowSignals))
	}

	return messages
}

func formatAlphaSignal(rank int, sig scanner.Signal) string {
	price := sig.YesPrice
	if sig.TargetSide == "NO" {
		price = sig.NoPrice
	}

	activityEmoji := activityEmoji(string(sig.Activity))
	tierLabel := "Tier B"
	if sig.Tier == scanner.TierA {
		tierLabel = "Tier A"
	}

	return fmt.Sprintf(
		`🟢 <b>ALPHA #%d — Score %.1f</b>

📌 <b>%s</b>
→ Buy <b>%s</b> @ $%.3f

💰 VWAP: $%.4f (slippage $%.4f)
📈 Depth: $%.0f | D/V: %.3f
↔️ Spread: %.1f%%
⏱ θ=%.2f (%d days) | %s %s
🏦 Liquidity: $%.0fK (%s)

<a href="%s">📎 View on Polymarket</a>`,
		rank,
		sig.Score,
		html.EscapeString(sig.Market.Question),
		sig.TargetSide,
		price,
		sig.FilteredMarket.VWAP,
		sig.FilteredMarket.SlippageUSD,
		sig.FilteredMarket.TrueDepthUSD,
		sig.DVRatio,
		sig.FilteredMarket.SpreadPercent,
		sig.ThetaMultiplier,
		sig.DaysToResolve,
		activityEmoji,
		string(sig.Activity),
		sig.Market.LiquidityNum/1000,
		tierLabel,
		sig.URL,
	)
}

// FormatShadowResults formats all shadow signals from the last scan as paginated messages.
func FormatShadowResults(result scanner.PipelineResult) []string {
	var shadowSignals []scanner.Signal
	for _, s := range result.Signals {
		if !s.IsAlpha() {
			shadowSignals = append(shadowSignals, s)
		}
	}

	if len(shadowSignals) == 0 {
		return []string{"👁 <b>No shadow signals</b> from the last scan."}
	}

	header := fmt.Sprintf("👁 <b>Shadow Signals</b> — %d markets monitored\n", len(shadowSignals))

	// Split into chunks to stay under Telegram's 4096-char limit (~15 signals per message)
	const chunkSize = 15
	var messages []string
	for i := 0; i < len(shadowSignals); i += chunkSize {
		end := i + chunkSize
		if end > len(shadowSignals) {
			end = len(shadowSignals)
		}
		chunk := shadowSignals[i:end]

		var sb strings.Builder
		if i == 0 {
			sb.WriteString(header)
		}
		for j, sig := range chunk {
			price := sig.YesPrice
			if sig.TargetSide == "NO" {
				price = sig.NoPrice
			}
			reasons := html.EscapeString(strings.Join(sig.ShadowReasons, ", "))
			sb.WriteString(fmt.Sprintf(
				"🔴 #%d <b>%s</b>\n→ %s $%.3f | D/V: %.3f\n❌ %s\n\n",
				i+j+1,
				html.EscapeString(truncate(sig.Market.Question, 60)),
				sig.TargetSide,
				price,
				sig.DVRatio,
				reasons,
			))
		}
		messages = append(messages, strings.TrimRight(sb.String(), "\n"))
	}
	return messages
}

func formatShadowBlock(signals []scanner.Signal) string {
	var sb strings.Builder
	sb.WriteString("👁 <b>Shadow Signals (monitor only)</b>\n\n")

	for i, sig := range signals {
		price := sig.YesPrice
		if sig.TargetSide == "NO" {
			price = sig.NoPrice
		}

		reasons := html.EscapeString(strings.Join(sig.ShadowReasons, ", "))

		sb.WriteString(fmt.Sprintf(
			"🔴 #%d <b>%s</b>\n→ %s $%.3f | D/V: %.3f\n❌ %s\n\n",
			i+1,
			html.EscapeString(truncate(sig.Market.Question, 55)),
			sig.TargetSide,
			price,
			sig.DVRatio,
			reasons,
		))
	}

	return strings.TrimRight(sb.String(), "\n")
}

func activityEmoji(status string) string {
	switch status {
	case "ACTIVE":
		return "🔥"
	case "NORMAL":
		return "✅"
	case "SLOW":
		return "🐢"
	case "DYING":
		return "💀"
	default:
		return "❓"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// FormatImportResults formats PillarLab import results as Telegram HTML messages.
func FormatImportResults(result analysis.ImportResult) []string {
	var messages []string

	if len(result.Matched) == 0 {
		return []string{"No predictions matched any scanned signals. Check that the market IDs align with the last /scan."}
	}

	// Sort by edge descending (best opportunities first)
	sorted := make([]analysis.MatchedSignal, len(result.Matched))
	copy(sorted, result.Matched)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Edge > sorted[j].Edge
	})

	// Count positive-edge signals
	positiveEdge := 0
	for _, m := range sorted {
		if m.Edge > 0 {
			positiveEdge++
		}
	}

	// Header
	header := fmt.Sprintf(
		`📊 <b>PillarLab Import Results</b>

├ Predictions: %d
├ Matched: %d
├ Positive edge: %d
└ Unmatched: %d`,
		len(result.Matched)+len(result.Unmatched),
		len(result.Matched),
		positiveEdge,
		len(result.Unmatched),
	)
	messages = append(messages, header)

	// Individual signals
	for i, m := range sorted {
		edgeEmoji := "🔴"
		if m.Edge > 0.10 {
			edgeEmoji = "🟢"
		} else if m.Edge > 0.05 {
			edgeEmoji = "🟡"
		} else if m.Edge > 0 {
			edgeEmoji = "🔵"
		}

		confEmoji := "❓"
		switch m.Prediction.Confidence {
		case "high":
			confEmoji = "🎯"
		case "medium":
			confEmoji = "📊"
		case "low":
			confEmoji = "⚠️"
		}

		msg := fmt.Sprintf(
			`%s <b>#%d — Edge %.1f%%</b>

📌 <b>%s</b>
→ Buy <b>%s</b> @ $%.3f
🧠 PillarLab: %.0f%% true prob | %s %s confidence
📈 Edge: $%.3f (%.1f%%)
🔢 Score: %.1f | θ=%.2f | D/V: %.3f

💬 <i>%s</i>`,
			edgeEmoji,
			i+1,
			m.Edge*100,
			html.EscapeString(truncate(m.Signal.Market.Question, 70)),
			m.OurSide,
			m.OurPrice,
			m.TrueProb*100,
			confEmoji,
			m.Prediction.Confidence,
			math.Abs(m.Edge),
			m.Edge*100,
			m.Signal.Score,
			m.Signal.ThetaMultiplier,
			m.Signal.DVRatio,
			html.EscapeString(truncate(m.Prediction.Reasoning, 120)),
		)
		messages = append(messages, msg)
	}

	// Unmatched predictions warning
	if len(result.Unmatched) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("⚠️ <b>%d unmatched predictions</b> (IDs not in last scan):\n", len(result.Unmatched)))
		for _, u := range result.Unmatched {
			sb.WriteString(fmt.Sprintf("• %s: %s\n", u.ID, html.EscapeString(truncate(u.Question, 50))))
		}
		messages = append(messages, sb.String())
	}

	return messages
}
