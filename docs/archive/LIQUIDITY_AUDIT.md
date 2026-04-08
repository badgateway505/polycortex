# Liquidity Engineering Audit — Roadmap Amendments

**Date:** 2026-03-25
**Context:** Discovered that Gamma API's `liquidityNum` is an aggregated/stale metric including "dead liquidity" (orders at $0.01/$0.99), whereas real execution requires "True Liquidity" near mid-price.

---

## Summary of Changes

| Refinement | Status Before | Status After | Milestone(s) Amended |
|------------|---------------|--------------|---------------------|
| **True Depth Calculation** | ⚠️ Gap | ✅ Fixed | 1.3 |
| **Price Impact / Slippage** | ⚠️ Gap | ✅ Fixed | 3.1 |
| **Mid-Price Entry Strategy** | 🟡 Implicit | ✅ Explicit | 5.1 |
| **Volume vs. Depth Validation** | ⚠️ Gap | ✅ Fixed | 1.4 |

---

## Amendment 1: True Depth Calculation (Milestone 1.3)

### What Changed:
**Before:**
- Vague "depth@bid" calculation — could be interpreted as total bid-side liquidity

**After:**
- **Explicit "True Depth"** = sum of bid/ask liquidity within ±2% of mid-price
- Ignore orders outside Golden Zone or >5% from mid-price
- Store both `totalLiquidity` (Gamma API) and `trueDepth` (CLOB book)
- Calculate depth imbalance: `(bidDepth - askDepth) / totalDepth` (detects buy/sell pressure)

### Why It Matters:
- **Gamma's `liquidityNum`** includes $311K at $0.99 and $43K at $0.01 (dead liquidity)
- **True Depth** shows only $45K within ±2% of $0.35 mid-price (actionable liquidity)
- Without this, bot might think a market is liquid when it's actually thin near execution price

### Implementation Added:
```go
// internal/scanner/depth.go
type TrueDepth struct {
    MidPrice       float64
    BidDepthUSD    float64 // Sum of bids within ±2% of mid
    AskDepthUSD    float64 // Sum of asks within ±2% of mid
    TotalDepthUSD  float64 // Bid + Ask
    DepthImbalance float64 // (Bid - Ask) / Total (negative = sell pressure)
}

func CalculateTrueDepth(book OrderBook, tolerance float64) TrueDepth {
    // Walk bids/asks, only count orders within ±2% of mid-price
    // Returns actionable depth, not total book depth
}
```

### Validation Gates Added:
- ✅ True depth calculation excludes orders >2% from mid-price
- ✅ Markets with high `totalLiquidity` but low `trueDepth` are flagged
- ✅ Depth imbalance calculation correct (positive = buy pressure, negative = sell pressure)

---

## Amendment 2: Volume vs. Depth Validation (Milestone 1.4)

### What Changed:
**Before:**
- No distinction between historical volume (hype) and active book depth (execution)

**After:**
- **New module:** `internal/scanner/liquidity_quality.go`
- **Calculate "Depth/Volume Ratio"** = `trueDepth / volume24h`
- Flag markets where volume is high but depth is low ("hype fade")
- **Prefer D/V ratio >0.05** (depth = 5%+ of daily volume)
- **Skip D/V <0.01** (illiquid despite volume)

### Why It Matters:
| Scenario | Volume | Depth | D/V Ratio | Interpretation |
|----------|--------|-------|-----------|----------------|
| **Healthy market** | $500K | $50K | 0.10 | Active + liquid |
| **Hype fade** | $500K | $2K | 0.004 | Was hot yesterday, dead now |
| **Stable LP** | $50K | $30K | 0.60 | Low churn, patient makers |
| **Dead market** | $5K | $500 | 0.10 | Both low, skip |

### Validation Gates Added:
- ✅ Depth/Volume ratio correctly identifies "hype fade" markets
- ✅ Markets with D/V <0.02 are flagged (can execute but risky)
- ✅ Markets with D/V <0.01 are skipped (illiquid despite volume)

---

## Amendment 3: Price Impact / Slippage Logic (Milestone 3.1)

### What Changed:
**Before:**
- Kelly sizing calculated without considering order book depth
- No calculation of "Effective Fill Price" for a specific order size

**After:**
- **New module:** `internal/sizing/impact.go`
- **Walk the order book** to calculate effective fill price for target size
- If order exceeds best level depth, calculate weighted average across multiple levels
- **Adjust edge:** `adjusted_edge = base_edge - price_impact`
- If impact >2%, reduce size or flag for manual approval

### Why It Matters:
**Example:**
- Kelly says: place $50 order at $0.28
- But order book depth at $0.28: only $30
- Remaining $20 fills at $0.281
- **Effective fill:** ($30 × 0.28 + $20 × 0.281) / $50 = $0.2804
- **Price impact:** +0.14% → edge reduction: 5.0% → 4.86%

Without this, bot might think it has 5% edge but actually only 4.86% after slippage.

### Implementation Added:
```go
// internal/sizing/impact.go
func CalculatePriceImpact(book OrderBook, side string, sizeUSD float64) (effectivePrice, impact float64) {
    // Walk ask side (for buys) or bid side (for sells)
    // Calculate weighted average fill price across multiple levels
    // Return effective price and impact percentage
    // If remaining > 0 after walking book → insufficient depth
}
```

### Validation Gates Added:
- ✅ Price impact calculation walks book correctly
- ✅ Edge adjusted for slippage when order size is significant
- ✅ If impact >2%, order size auto-reduced or flagged for approval
- ✅ If insufficient depth to fill order, reject with clear message

---

## Amendment 4: Mid-Price Entry Strategy (Milestone 5.1)

### What Changed:
**Before:**
- Mentioned "best_bid + $0.001" in Milestone 4.3 (slippage tolerance context)
- Not explicit in execution logic

**After:**
- **Explicit order placement strategy:**
  - **BUY orders:** `best_bid + $0.001` (queue jump)
  - **SELL orders:** `best_ask - $0.001`
  - **Why $0.001:** Increases fill rate ~3× for negligible cost (~0.3% = maker rebate neutralizes it)
  - Captures spread while remaining maker (saves ~2% vs. taker fees)
  - Maker rebate (-0.3%) already included in Kelly `b` calculation

### Why It Matters:
- **Without queue jump:** Order sits at back of queue at $0.35, may never fill
- **With $0.001 jump:** Order at $0.351, fills 3× faster, cost neutralized by maker rebate
- **Mathematical advantage:** Spread capture + maker rebate = ~2.3% edge boost vs. taker

### Validation Gates Added:
- ✅ Order placed at `best_bid + $0.001` (not best_bid, not best_bid + $0.01)
- ✅ Order is maker (not taker) — verify fee is negative (rebate)

---

## Critical Dependency: Order Book Freshness

All liquidity engineering improvements depend on **fresh order book data**:

**Current Approach (Phase 1-3):**
- REST API polling (`GET /book`) with 30s cache
- Acceptable for scanning/filtering
- **Recalculate depth JUST before order submission** (Phase 5)

**Future (Phase 6):**
- WebSocket feed for real-time book updates
- If CLOB REST API is too stale, move WebSocket earlier (Milestone 1.3 kill condition)

---

## Pro-Tips Added to Roadmap

### For Depth Calculation (`internal/scanner/depth.go`):
1. **Pre-allocate slices** if parsing 100+ books per scan (performance)
2. **Cache book snapshots** with 30s TTL (avoid hammering CLOB API)
3. **For execution:** recalculate depth JUST before order submission (freshness)

### For Price Impact (`internal/sizing/impact.go`):
1. **Walk book efficiently:** stop when remaining size = 0
2. **If insufficient depth:** return `impact = 999.0` (signal: reject order)
3. **Test with edge cases:** empty book, single level, insufficient depth

---

## Testing Strategy

### Unit Tests (Milestone 3.1):
```go
func TestPriceImpact(t *testing.T) {
    book := OrderBook{
        Asks: []Level{
            {Price: 0.28, Size: 107.14},  // $30 worth
            {Price: 0.281, Size: 71.17},  // $20 worth
            {Price: 0.282, Size: 177.30}, // $50 worth
        },
    }

    effectivePrice, impact := CalculatePriceImpact(book, "BUY", 50.0)

    // Expected: ($30 × 0.28 + $20 × 0.281) / $50 = $0.2804
    assert.InDelta(t, 0.2804, effectivePrice, 0.0001)

    // Expected impact: (0.2804 - 0.28) / 0.28 = 0.00143 = 0.14%
    assert.InDelta(t, 0.00143, impact, 0.0001)
}
```

### Integration Tests (Milestone 1.3):
1. Fetch real order book for a known market (e.g., Hungary PM)
2. Compare `totalLiquidity` (Gamma) vs. `trueDepth` (CLOB within ±2%)
3. Verify depth imbalance matches visual inspection of book
4. Spot-check against Polymarket website

### Live Validation (Phase 5):
1. Track **expected fill price** (from impact calculator) vs. **actual fill price** (from CLOB trade history)
2. If divergence >1%, investigate (stale book data, fast-moving market, etc.)
3. Calibrate impact model based on real fills over 1+ week

---

## Success Metrics

### Phase 1 (Scanning):
- ✅ True depth calculation finds 10-15 markets with >$10K depth within ±2% of mid
- ✅ Depth/Volume ratio correctly identifies 2-3 "hype fade" markets per scan
- ✅ Depth imbalance correlates with observed buy/sell pressure

### Phase 3 (Paper Trading):
- ✅ Price impact model predicts slippage within ±0.5% of simulated fills
- ✅ Edge adjustments prevent overestimating profitability on thin books

### Phase 5 (Live Trading):
- ✅ Actual fill prices match expected fill prices within ±0.5%
- ✅ No accidental taker fees (all orders are maker with rebate)
- ✅ Queue jump strategy ($0.001) achieves 3× faster fills vs. best_bid exactly

---

## Cost/Benefit Analysis

| Refinement | Implementation Time | Risk Mitigated | Value |
|------------|---------------------|----------------|-------|
| **True Depth** | +1 hour (Milestone 1.3) | Avoid thin markets disguised as liquid | 🔴 **Critical** |
| **Price Impact** | +2 hours (Milestone 3.1) | Prevent slippage from destroying edge | 🔴 **Critical** |
| **Volume vs. Depth** | +1 hour (Milestone 1.4) | Filter out "hype fade" markets | 🟠 **Important** |
| **Mid-Price Entry** | +0 hours (clarification only) | Maximize fill rate + capture spread | 🟡 **Already covered** |

**Total Additional Time:** ~4 hours (spread across Milestones 1.3, 1.4, 3.1)

**Risk Reduction:** Prevents 2-5% edge erosion from slippage/poor liquidity

**ROI:** If bot makes 10 trades/week, saving 2% per trade = 20% edge preservation = $20-50/week on $1K bankroll

---

## Next Steps

1. **Implement Milestone 1.3 amendments** (True Depth calculation)
2. **Test with real order books** (Hungary PM, Avalanche SC, etc.)
3. **Validate depth vs. Polymarket website** (spot-check 3-5 markets)
4. **Proceed to Milestone 1.4** (add Depth/Volume ratio filter)
5. **Defer impact.go until Milestone 3.1** (sizing phase)

---

**Last Updated:** 2026-03-25
**Status:** Roadmap amended, ready for implementation
