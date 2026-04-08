package scanner

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
)

// RunPipeline executes the full L1→L2→L3→L4 filter pipeline.
//
// clob may be nil — if so, L4 runs in degraded mode and routes all L3
// survivors to Shadow with reason "CLOB_UNAVAILABLE".
func RunPipeline(markets []polymarket.Market, cfg *config.Config, clob *polymarket.CLOBClient, logger *slog.Logger) PipelineResult {
	if logger == nil {
		logger = slog.Default()
	}

	result := PipelineResult{
		TotalScanned: len(markets),
		AllRejects:   make([]Rejection, 0),
	}

	logger.Info("Starting filter pipeline",
		slog.Int("total_markets", len(markets)))

	// L1: Category Gate
	l1Passed, l1Result := L1CategoryGate(markets, cfg, logger)
	result.L1Result = l1Result
	result.AllRejects = append(result.AllRejects, l1Result.Rejects...)

	if len(l1Passed) == 0 {
		logger.Warn("L1 filtered out all markets")
		return result
	}

	// L2: Liveness Check
	l2Passed, l2Result := L2LivenessCheck(l1Passed, cfg, logger)
	result.L2Result = l2Result
	result.AllRejects = append(result.AllRejects, l2Result.Rejects...)

	if len(l2Passed) == 0 {
		logger.Warn("L2 filtered out all markets")
		return result
	}

	// L3: Quality Gate
	l3Passed, l3Result := L3QualityGate(l2Passed, cfg, logger)
	result.L3Result = l3Result
	result.AllRejects = append(result.AllRejects, l3Result.Rejects...)
	result.Passed = l3Passed

	if len(l3Passed) == 0 {
		logger.Warn("L3 filtered out all markets")
		return result
	}

	// L4: Distribution Engine (requires live CLOB order book data)
	signals, l4Result := L4DistributionEngine(l3Passed, clob, cfg, logger)
	result.L4Result = l4Result
	result.Signals = signals

	logger.Info("Pipeline complete",
		slog.Int("total_scanned", result.TotalScanned),
		slog.Int("l1_passed", result.L1Result.Passed),
		slog.Int("l2_passed", result.L2Result.Passed),
		slog.Int("l3_passed", result.L3Result.Passed),
		slog.Int("l4_alpha", result.L4Result.Passed),
		slog.Int("l4_shadow", result.L4Result.Rejected))

	logRejectBreakdown(logger, result)

	return result
}

// logRejectBreakdown logs a human-readable reject breakdown for each layer.
func logRejectBreakdown(logger *slog.Logger, result PipelineResult) {
	layers := []struct {
		name   string
		result LayerResult
	}{
		{"L1", result.L1Result},
		{"L2", result.L2Result},
		{"L3", result.L3Result},
		{"L4", result.L4Result},
	}

	for _, l := range layers {
		if len(l.result.RejectCounts) == 0 {
			continue
		}

		// Sort reasons by count descending for readability
		type kv struct {
			reason string
			count  int
		}
		pairs := make([]kv, 0, len(l.result.RejectCounts))
		for r, c := range l.result.RejectCounts {
			pairs = append(pairs, kv{r, c})
		}
		sort.Slice(pairs, func(i, j int) bool {
			return pairs[i].count > pairs[j].count
		})

		parts := make([]string, len(pairs))
		for i, p := range pairs {
			parts[i] = fmt.Sprintf("%s×%d", p.reason, p.count)
		}
		logger.Info(fmt.Sprintf("%s reject breakdown", l.name),
			slog.String("reasons", strings.Join(parts, " | ")))
	}
}
