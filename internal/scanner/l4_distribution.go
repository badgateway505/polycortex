package scanner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/polymarket"
)

// L4DistributionEngine filters L3 survivors using live CLOB order book data.
//
// For each market it:
//  1. Identifies which outcome (YES/NO) is in the Golden Zone
//  2. Fetches that outcome's CLOB order book
//  3. Checks spread (≤max_spread_pct)
//  4. Checks D/V ratio — hard skip if <0.01 (illiquid despite volume)
//  5. Calculates VWAP at max stake to verify the real fill price is in zone
//  6. Checks true depth (≥min_true_depth_usd actionable within ±2%)
//  7. Computes theta decay, activity score, and composite ranking score
//
// Route: Alpha = all checks pass | Shadow = any check fails
// Shadow markets are returned too — useful for monitoring and backtesting.
// Alpha signals are sorted by Score descending (best candidates first).
//
// Concurrency: fetches up to 5 order books in parallel (CLOB rate limit safe).
func L4DistributionEngine(markets []FilteredMarket, clob *polymarket.CLOBClient, cfg *config.Config, logger *slog.Logger) ([]Signal, LayerResult) {
	result := LayerResult{
		LayerName:    "L4_DISTRIBUTION_ENGINE",
		RejectCounts: make(map[string]int),
		Rejects:      make([]Rejection, 0),
	}

	if len(markets) == 0 {
		return nil, result
	}

	// No CLOB client = graceful degradation — shadow everything
	if clob == nil {
		logger.Warn("L4: no CLOB client — routing all markets to Shadow")
		signals := make([]Signal, len(markets))
		for i, m := range markets {
			signals[i] = Signal{
				FilteredMarket: m,
				Route:          Shadow,
				ShadowReasons:  []string{"CLOB_UNAVAILABLE"},
			}
			result.Rejected++
			result.RejectCounts["CLOB_UNAVAILABLE"]++
		}
		return signals, result
	}

	stakeUSD := cfg.Distribution.DefaultBalance * cfg.Distribution.DefaultStakePct

	type work struct {
		idx    int
		market FilteredMarket
	}
	type res struct {
		idx    int
		signal Signal
	}

	workCh := make(chan work, len(markets))
	resCh := make(chan res, len(markets))

	// Cap at 5 concurrent workers to stay under CLOB rate limits
	workerCount := 5
	if len(markets) < workerCount {
		workerCount = len(markets)
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
				sig := runL4Checks(w.market, clob, cfg, stakeUSD, logger)
				resCh <- res{idx: w.idx, signal: sig}
			}
		}()
	}

	for i, m := range markets {
		workCh <- work{idx: i, market: m}
	}
	close(workCh)

	go func() {
		wg.Wait()
		close(resCh)
	}()

	signals := make([]Signal, len(markets))
	for r := range resCh {
		signals[r.idx] = r.signal
	}

	// Tally results
	for _, sig := range signals {
		if sig.Route == Alpha {
			result.Passed++
		} else {
			result.Rejected++
			for _, reason := range sig.ShadowReasons {
				// Strip detail in parens for clean bucketing: "THIN_DEPTH ($50<$250)" → "THIN_DEPTH"
				key := strings.SplitN(reason, " (", 2)[0]
				result.RejectCounts[key]++
			}
		}
	}

	// Sort Alpha signals by Score descending (best candidates first)
	// Shadow signals follow, unsorted (for monitoring)
	sort.SliceStable(signals, func(i, j int) bool {
		iAlpha := signals[i].Route == Alpha
		jAlpha := signals[j].Route == Alpha
		if iAlpha != jAlpha {
			return iAlpha // Alpha before Shadow
		}
		return signals[i].Score > signals[j].Score
	})

	logger.Info("L4 Distribution Engine complete",
		slog.Int("total", len(markets)),
		slog.Int("alpha", result.Passed),
		slog.Int("shadow", result.Rejected))

	return signals, result
}

// runL4Checks processes a single market through all L4 checks and returns a Signal.
func runL4Checks(m FilteredMarket, clob *polymarket.CLOBClient, cfg *config.Config, stakeUSD float64, logger *slog.Logger) Signal {
	sig := Signal{
		FilteredMarket: m,
		ShadowReasons:  make([]string, 0),
	}

	// Theta decay — computed from days to resolution regardless of CLOB result
	sig.ThetaMultiplier = CalculateThetaDecay(m.DaysToResolve, cfg.ThetaDecay)

	// Activity scoring — uses volume data from Gamma API (no CLOB needed)
	activity, _ := CalculateActivityScore(m.Market.Volume24h, m.Market.Volume1Wk)
	sig.Activity = activity

	// Determine which outcome (YES/NO) to analyze
	targetSide, tokenID := selectGoldenZoneSide(m)
	if tokenID == "" {
		sig.Route = Shadow
		sig.ShadowReasons = append(sig.ShadowReasons, "NO_TOKEN_ID")
		logger.Debug("L4: no token ID found",
			slog.String("market_id", m.Market.ID))
		return sig
	}

	sig.TargetSide = targetSide

	// Populate token IDs on the FilteredMarket for downstream use
	yesID, noID := parseClobTokenIds(m.Market.ClobTokenIds)
	sig.FilteredMarket.YesTokenID = yesID
	sig.FilteredMarket.NoTokenID = noID

	// Fetch order book
	book, err := clob.GetOrderBook(tokenID)
	if err != nil {
		sig.Route = Shadow
		sig.ShadowReasons = append(sig.ShadowReasons, "CLOB_ERROR")
		logger.Warn("L4: CLOB fetch failed",
			slog.String("market_id", m.Market.ID),
			slog.String("token_id", tokenID),
			slog.String("error", err.Error()))
		return sig
	}

	snapshot, err := polymarket.ParseBookSnapshot(book)
	if err != nil {
		sig.Route = Shadow
		sig.ShadowReasons = append(sig.ShadowReasons, "EMPTY_BOOK")
		logger.Debug("L4: empty or unparseable book",
			slog.String("market_id", m.Market.ID),
			slog.String("error", err.Error()))
		return sig
	}

	// Populate best bid/ask on FilteredMarket
	sig.FilteredMarket.BestBid = snapshot.BestBid
	sig.FilteredMarket.BestAsk = snapshot.BestAsk
	sig.FilteredMarket.Spread = snapshot.Spread
	sig.FilteredMarket.SpreadPercent = snapshot.SpreadPercent

	// Check 1: Spread ≤ max_spread_pct
	if snapshot.SpreadPercent > cfg.Distribution.MaxSpreadPct {
		sig.ShadowReasons = append(sig.ShadowReasons,
			fmt.Sprintf("WIDE_SPREAD (%.1f%%>%.1f%%)", snapshot.SpreadPercent, cfg.Distribution.MaxSpreadPct))
	}

	// Check 2: VWAP — real fill price must land in Golden Zone
	vwapResult, err := CalculateVWAP(snapshot, "BUY", stakeUSD)
	if err != nil {
		sig.ShadowReasons = append(sig.ShadowReasons, "VWAP_ERROR")
	} else {
		sig.FilteredMarket.VWAP = vwapResult.VWAP
		sig.FilteredMarket.SlippageUSD = vwapResult.Slippage
		sig.VWAPResult = vwapResult

		if !vwapResult.Sufficient {
			sig.ShadowReasons = append(sig.ShadowReasons, "INSUFFICIENT_LIQUIDITY")
		} else if vwapResult.VWAP < cfg.Distribution.GoldenZoneMin || vwapResult.VWAP > cfg.Distribution.GoldenZoneMax {
			sig.ShadowReasons = append(sig.ShadowReasons,
				fmt.Sprintf("VWAP_OUT_OF_ZONE ($%.4f)", vwapResult.VWAP))
		}
	}

	// Check 3: True depth ≥ min_true_depth_usd
	depth, err := CalculateTrueDepthDefault(snapshot)
	if err != nil {
		sig.ShadowReasons = append(sig.ShadowReasons, "DEPTH_ERROR")
	} else {
		sig.FilteredMarket.TrueDepthUSD = depth.TotalDepthUSD
		sig.DepthResult = depth

		if depth.TotalDepthUSD < cfg.Distribution.MinTrueDepthUSD {
			sig.ShadowReasons = append(sig.ShadowReasons,
				fmt.Sprintf("THIN_DEPTH ($%.0f<$%.0f)", depth.TotalDepthUSD, cfg.Distribution.MinTrueDepthUSD))
		}

		// Check 4: D/V ratio — hard skip if market is illiquid despite volume
		// D/V < 0.01 = "hype fade" — volume was there but depth has dried up
		if m.Market.Volume24h > 0 {
			sig.DVRatio = depth.TotalDepthUSD / m.Market.Volume24h
		}
		lq := AssessLiquidityQuality(m.Market.LiquidityNum, m.Market.Volume24h, depth)

		if lq.IsHypeFade {
			sig.ShadowReasons = append(sig.ShadowReasons,
				fmt.Sprintf("HYPE_FADE (D/V=%.3f)", sig.DVRatio))
		} else if sig.DVRatio > 0 && sig.DVRatio < 0.01 {
			// Hard skip: illiquid despite volume
			sig.ShadowReasons = append(sig.ShadowReasons,
				fmt.Sprintf("ILLIQUID_DV (%.3f<0.01)", sig.DVRatio))
		}
	}

	// Final routing
	if len(sig.ShadowReasons) == 0 {
		sig.Route = Alpha
	} else {
		sig.Route = Shadow
	}

	// Compute composite score (meaningful for Alpha; computed for all for monitoring)
	sig.Score = CalculateCompositeScore(
		sig.ThetaMultiplier,
		sig.DVRatio,
		sig.FilteredMarket.SpreadPercent,
		cfg.Distribution.MaxSpreadPct,
	)

	logger.Info("L4: market analyzed",
		slog.String("market_id", m.Market.ID),
		slog.String("route", string(sig.Route)),
		slog.String("side", targetSide),
		slog.String("activity", string(activity)),
		slog.Float64("theta", sig.ThetaMultiplier),
		slog.Float64("vwap", sig.FilteredMarket.VWAP),
		slog.Float64("dv_ratio", sig.DVRatio),
		slog.Float64("depth_usd", sig.FilteredMarket.TrueDepthUSD),
		slog.Float64("spread_pct", sig.FilteredMarket.SpreadPercent),
		slog.Float64("score", sig.Score))

	return sig
}

// selectGoldenZoneSide returns the outcome name and CLOB token ID for the Golden Zone outcome.
// Prefers YES if both are in zone (unlikely but possible).
func selectGoldenZoneSide(m FilteredMarket) (side string, tokenID string) {
	yesID, noID := parseClobTokenIds(m.Market.ClobTokenIds)

	if m.YesPrice >= 0.20 && m.YesPrice <= 0.40 {
		return "YES", yesID
	}
	if m.NoPrice >= 0.20 && m.NoPrice <= 0.40 {
		return "NO", noID
	}

	// Shouldn't reach here — L3 already verified Golden Zone
	return "", ""
}

// parseClobTokenIds decodes the Gamma API's clobTokenIds JSON string.
// Format: "[\"yesTokenID\", \"noTokenID\"]"
// Returns empty strings if parsing fails.
func parseClobTokenIds(raw string) (yesID, noID string) {
	if raw == "" {
		return "", ""
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return "", ""
	}
	if len(ids) >= 1 {
		yesID = ids[0]
	}
	if len(ids) >= 2 {
		noID = ids[1]
	}
	return yesID, noID
}
