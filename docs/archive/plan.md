# Implementation Plan: "Golden Waterfall"

Structured for rapid iteration: **ship a working vertical slice first, then deepen each layer.**

---

## Technical Stack

| Component | Tool / Technology | Status | Justification |
| :--- | :--- | :--- | :--- |
| **Execution Language** | **Golang 1.25+** | `✅` | Superior concurrency (goroutines) and low-latency JSON processing. |
| **API Client** | **Custom REST + `go-order-utils`** | `✅ Verified` | No official Go CLOB client exists. Build lean REST wrappers + use official `go-order-utils` for EIP-712 signing. Reference `py-clob-client` for API contracts. |
| **Layer 1 (Signal)** | **PillarLab AI (manual)** | `✅ Verified` | 64% win rate via 14-pillar framework. General LLMs only achieve 39-42%. $29/mo (150 credits). No public API — operated manually via chat with structured prompt templates, results fed to bot via Telegram. |
| **Layer 2 (Logic/Trust)** | **Claude 4.5 Sonnet API** | `✅` | Extended Thinking for resolution trap detection, source trust scoring (4 levels), conflict of interest checks, market integrity analysis, contract language scanning. |
| **Layer 3 (News/Intel)** | **Market Internals (free) + Tavily ultra-fast (fallback)** | `✅ Verified` | Primary: category divergence check + correlated pairs analysis (free, uses existing API data). Fallback: Tavily ($0.008/search) only when market internals are inconclusive. |
| **Interface** | **Telegram Bot** | `✅` | Human-in-loop signal input (`/import`), trade approval, position monitoring, P&L alerts. |
| **Database (Dev)** | **SQLite** | `✅` | Trade logs, market snapshots, correlation maps, backtesting data. |
| **Database (Prod)** | **Redis** | `` | In-memory order book cache for sub-ms reads. |
| **Hosting (Prod)** | **AWS Dublin (eu-west-1)** | `` | Sub-1ms latency to London-based matching engine. |

---

## Waterfall Pipeline Architecture

```
DAILY SCAN (Automated — 500+ markets, ~60 seconds)
    │
    ▼
┌──────────────────────────────────────────────────┐
│  Pre-Filter: Market Selection                    │
│                                                  │
│  • Golden Zone: $0.20 – $0.40                    │
│  • Market Status:                                │
│      Check isResolved, isClosed flags            │
│      (markets can close before endDate if        │
│       event occurs early)                        │
│      Verify order book is not empty              │
│  • Stale filter: endDate > now                   │
│  • Liquidity Tiering:                            │
│      Tier A (>$50K): full allocation, can exit   │
│      Tier B ($5-50K): hold-to-resolution only    │
│      Skip: <$5K (too thin)                       │
│  • Spread: >3¢ bid-ask                           │
│  • Activity: last trade <24h (Tier B required)   │
│  • Order Book Depth Check:                       │
│      If Kelly size > depth at target price        │
│      → flag "thin book", reduce size or warn     │
│  • Price stability: flag if >5% move/1h          │
│  → ~20-30 candidates                             │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  Layer 2: Claude 4.5 (Logic, Trust & Integrity)  │
│  [Runs BEFORE PillarLab to pre-score markets]    │
│                                                  │
│  • Source Trust Score (4 levels):                 │
│      L1 (0.95): Official authority (AP, FIFA)    │
│      L2 (0.75): Credible institution (Reuters)   │
│      L3 (0.50): Vague sourcing ("major media")   │
│      L4 (0.25): Conflict of interest / self-report│
│                                                  │
│  • Contract Language Red Flags:                  │
│      "at the discretion of"                      │
│      "reports indicate", "generally acknowledged" │
│      "widely reported", "credible sources"        │
│                                                  │
│  • Green Flags:                                  │
│      "solely determined by", "official results"  │
│      "certified by", "as published by [name]"    │
│                                                  │
│  • Compound Condition Detector:                  │
│      2+ conditions (X AND Y, UNLESS Z)           │
│      → automatic 0.5 position modifier           │
│                                                  │
│  • Conflict of Interest:                         │
│      Source benefits from outcome?                │
│      Token reporting own price via own oracle?    │
│      Project self-certifying milestones?          │
│      → UMA dispute risk flag                     │
│                                                  │
│  • Market Integrity Check (NEW):                 │
│      Analyze YES vs NO condition asymmetry       │
│      "YES requires A AND B AND C simultaneously" │
│      "NO if ANY single condition fails"           │
│      → This shifts expected value — PillarLab    │
│        may give "clean" probability without      │
│        accounting for conditional asymmetry      │
│      → Adjust position modifier accordingly      │
│                                                  │
│  → ~15-20 scored candidates with clarity_score   │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  Layer 1: PillarLab (Signal Generation)          │
│  [MANUAL — human pastes top 10-15 candidates]    │
│                                                  │
│  • Pre-configured prompt template with JSON out  │
│  • "True Probability" estimate per market        │
│  • Edge = TrueProb - MarketPrice                 │
│  • PASS if edge > 3%                             │
│  • Copy output → Telegram /import command        │
│  → ~5-8 with confirmed edge                      │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  STALE PRICE CHECK (Automated — on /import)      │
│  [NEW: catches price drift during research]      │
│                                                  │
│  For each imported market:                       │
│    Re-fetch current price from CLOB              │
│    Compare to price when scan was generated      │
│    If price moved OUT of Golden Zone (>$0.40     │
│    or <$0.20) during PillarLab research time:    │
│      → ⚠️ "PRICE MOVED: was $0.28, now $0.42    │
│         — SKIPPING, no longer in Golden Zone"    │
│    If price moved >3% but still in zone:         │
│      → ⚠️ "PRICE DRIFTED: was $0.28, now $0.32  │
│         — recalculate edge with new price"       │
│                                                  │
│  → Recalculated edges, stale candidates removed  │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  Layer 3: News & Market Intelligence             │
│  [Automated — runs after stale check]            │
│                                                  │
│  A) Category Divergence (FREE):                  │
│     Our market dropped 10% but category flat?    │
│     → Market-specific news, DON'T BUY            │
│     Whole category dropped?                      │
│     → Systemic noise, possible DCA opportunity   │
│                                                  │
│  B) Correlated Pairs (FREE):                     │
│     Linked market unchanged while ours dropped?  │
│     → Fat finger / arb opportunity               │
│     Both dropped together?                       │
│     → Real news, HOLD                            │
│                                                  │
│  C) Tavily Ultra-Fast (FALLBACK, $0.008):        │
│     Only if A+B inconclusive                     │
│     → PASS if no breaking catalyst               │
│                                                  │
│  → ~3-5 final candidates                         │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  POSITION SIZING                                 │
│                                                  │
│  Profit = (Payout - Price) - Fee(maker)          │
│  Fee(maker) is often NEGATIVE (rebate) or zero   │
│                                                  │
│  Kelly formula with maker rebate baked in:       │
│    b = (Payout - Entry - MakerFee) / Entry       │
│      where MakerFee < 0 means rebate → b grows  │
│    Kelly% = (p × (b + 1) - 1) / b               │
│    Fractional Kelly = Kelly% × 0.25              │
│                                                  │
│  size = bankroll × fractional_kelly              │
│    × clarity_score      (trust + flags +         │
│                          integrity + conflict)   │
│    × liquidity_modifier (Tier A:1.0, B:0.5)     │
│    × theta              (time decay, prefer fast)│
│    × dca_level          (1.0 → 0.5 → 0.25)      │
│                                                  │
│  Theta (time decay):                             │
│    ≤7 days:   1.00  (maximum allocation)         │
│    ≤14 days:  0.90                               │
│    ≤30 days:  0.75                               │
│    ≤60 days:  0.50                               │
│    ≤180 days: 0.25                               │
│    >180 days: 0.10  (barely worth it)            │
│                                                  │
│  Constraints:                                    │
│    min_size = $5, max_size = 15% of bankroll     │
│    max 5 concurrent positions                    │
│    Tier B lockup ≤ 40% of bankroll               │
│    Tier B resolution ≤ 30 days                   │
│                                                  │
│  → User approves via Telegram /approve           │
└──────────────┬───────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────┐
│  EXECUTION (Maker Orders Only)                   │
│                                                  │
│  Pre-check: verify order book depth vs size      │
│    If depth at target < Kelly size:              │
│      → reduce to available depth                 │
│      → or warn "thin book, partial fill likely"  │
│                                                  │
│  Tier A: limit at best_bid + $0.001              │
│    → Queue jump hack, 3× fill rate               │
│    → Negligible cost ($0.001/share)              │
│                                                  │
│  Tier B: limit at best_bid, 12h expiry           │
│    → Patient, skip if market dead (>24h)         │
│                                                  │
│  NEVER market orders. NEVER taker fees.          │
│  Accidentally hitting book (taker) at 0.2-0.4   │
│  prices has very high fee sensitivity —          │
│  can destroy the entire edge.                    │
└──────────────────────────────────────────────────┘

              ┌─────────────┐
              │  CONTINUOUS  │
              │  MONITORING  │
              │  (30 min)    │
              └──────┬──────┘
                     │
    ┌────────────────┼────────────────┐
    ▼                ▼                ▼
┌────────┐   ┌────────────┐   ┌────────────┐
│REBALANCE│   │PROFIT TAKE │   │RISK MGMT   │
│(DCA)    │   │(Tier A)    │   │            │
│         │   │            │   │Circuit     │
│Drop >5% │   │+33% entry: │   │Breakers:   │
│no news? │   │ sell 30%   │   │ 3 losses → │
│→ buy    │   │+67% entry: │   │  halt 24h  │
│  more   │   │ sell 30%   │   │ 5% DD →    │
│  (0.5×) │   │Hold 40% to │   │  halt 24h  │
│         │   │ resolution │   │            │
│Max 3    │   │            │   │Volatility: │
│entries  │   │            │   │ BTC/ETH >2%│
│Never >2×│   │            │   │ in 5min →  │
│initial  │   │            │   │ cancel all │
│         │   │            │   │            │
│STOP DCA │   │            │   │Capital     │
│if <$0.15│   │            │   │Lockup:     │
│(insight │   │            │   │ Tier B ≤40%│
│already  │   │            │   │ of bankroll│
│priced   │   │            │   │ max 30d    │
│in)      │   │            │   │ resolution │
│         │   │            │   │            │
│         │   │            │   │UMA Dispute │
│         │   │            │   │Monitor:    │
│         │   │            │   │ Check open │
│         │   │            │   │ disputes   │
│         │   │            │   │ for held   │
│         │   │            │   │ markets    │
│         │   │            │   │ Dispute →  │
│         │   │            │   │ ALERT +    │
│         │   │            │   │ recommend  │
│         │   │            │   │ exit (Tier │
│         │   │            │   │ A only)    │
└────────┘   └────────────┘   └────────────┘
```

---

## Implementation Phases (Move Fast, Break Things)

---

### Phase 1: Scanner + Telegram Bot — "Hello Polymarket"

**Goal:** Go binary that scans 500+ markets, filters Golden Zone, and exposes a Telegram bot for signal input and monitoring.

**Scanner:**
- `cmd/poly/main.go` — entry point, config loading, daily scan scheduler
- `internal/polymarket/gamma.go` — Gamma API client: fetch markets, paginate
- `internal/polymarket/clob.go` — CLOB REST client: fetch order books, spreads
- `internal/scanner/filter.go` — Pre-filter pipeline:
  - Golden Zone: price $0.20–$0.40 (client-side, API doesn't support price filter)
  - Market status: check `closed`, `active` fields. Verify order book is not empty (markets can close before `endDate` if event resolves early)
  - Stale filter: `endDate > now`
  - Liquidity tiering: Tier A (>$50K), Tier B ($5-50K), Skip (<$5K)
  - Spread filter: bid-ask > 3 cents
  - Activity check: last trade timestamp (skip dead markets >24h for Tier B)
  - Order book depth check: compare Kelly-sized order vs. available depth at target price. Flag "thin book" if depth < desired size
  - Price movement: flag >5% change in last hour
- `internal/scanner/theta.go` — Time decay calculation (days to resolution → modifier)
- `internal/scanner/depth.go` — Order book depth analysis: compare desired position size vs. available liquidity at target price level

**Telegram Bot:**
- `internal/telegram/bot.go` — Bot framework with command router
- Commands:
  - `/scan` — trigger manual scan, output Golden Zone candidates with tier + theta + depth + activity labels
  - `/import <JSON>` — parse PillarLab structured output, run stale price check + Layer 2+3
  - `/status` — show open positions, unrealized P&L, account balance, Tier B lockup %
  - `/help` — list all commands

**Config:**
- `.env` — API keys (Polymarket, Claude, Tavily, Telegram bot token)
- `config.yaml` — thresholds (Golden Zone bounds, liquidity tiers, theta table, Kelly fraction, lockup limits)

**Data:**
- `internal/store/sqlite.go` — SQLite for market snapshots, scan history, PillarLab prediction log
- `golden_zone_research.json` — seed data from initial research (22 markets)

**PillarLab Prediction Tracking:**
- `internal/store/predictions.go` — Log every PillarLab prediction for accuracy tracking:
  - On `/import`: store `{ market_id, question, pillarlab_probability, pillarlab_edge, market_price_at_import, our_side (YES/NO), confidence, timestamp }`
  - On market resolution: store `{ actual_outcome (YES/NO), resolution_price, pnl }`
  - Telegram command `/accuracy` → shows PillarLab's track record:
    - Total predictions, win rate, avg edge predicted vs. actual, calibration curve
    - "PillarLab predicted 64 markets. Won 38 (59.4%). Avg predicted edge: 5.1%. Actual edge: 3.2%."
  - This validates (or disproves) the 64% win rate claim with YOUR data, not their marketing

**PillarLab prompt template:**
- `templates/pillarlab_prompt.md` — pre-configured prompt that forces structured JSON output

**Result:** Run `/scan` in Telegram → see filtered Golden Zone markets with tier/theta/depth/activity labels.

---

### Phase 2: Waterfall Engine — "Smart Signals"

**Goal:** Wire up Claude analysis (Layer 2) and market intelligence (Layer 3). PillarLab output parsed from Telegram `/import`.

**Layer 2 — Claude Logic, Trust & Market Integrity:**
- `internal/analysis/claude.go` — Claude API integration with Extended Thinking
- `internal/analysis/trust.go` — Source trust scoring engine:
  - Parse resolution source from market metadata
  - Score trust level (1-4) based on source identity
  - Scan contract text for red/green flag marker words
  - Detect compound conditions (2+ → 0.5 modifier)
  - Flag conflict of interest (source benefits from outcome)
- `internal/analysis/integrity.go` — Market integrity checker:
  - Analyze YES vs NO condition asymmetry
  - Detect "YES requires all of A AND B AND C" vs "NO if any single condition fails"
  - Calculate expected value skew from conditional asymmetry
  - Adjust position modifier to compensate for skew PillarLab may miss
- Output: `ClarityScore` struct (trust_level, trust_score, red_flags, green_flags, compound_conditions, conflict_detected, integrity_skew, position_modifier)

**Stale Price Check (on /import):**
- `internal/pipeline/stalecheck.go` — Re-fetch current CLOB price for each imported market
  - Compare to price at scan time (stored in SQLite from Phase 1)
  - If price moved out of Golden Zone during PillarLab research: SKIP with warning
  - If price drifted >3% but still in zone: recalculate edge with fresh price
  - Telegram alert: "⚠️ PRICE MOVED: was $0.28, now $0.42 — SKIPPING" or "⚠️ PRICE DRIFTED: was $0.28, now $0.32 — edge recalculated: 4.1% → 2.9%"

**Layer 3 — Market Intelligence:**
- `internal/analysis/category.go` — Category divergence detector:
  - Track average price change per category
  - Compare individual market move vs. category average
  - If divergence > 5% → market-specific news signal
- `internal/analysis/correlation.go` — Correlated pairs engine:
  - Maintain correlation map (stored in SQLite)
  - Detect broken correlations (one market moves, linked market doesn't)
  - Flag: real news vs. fat finger vs. arbitrage opportunity
- `internal/analysis/tavily.go` — Tavily fallback (only when internals inconclusive)
  - Ultra-fast mode, `topic: "news"`
  - Threshold: only call if category + correlation checks are MIXED_SIGNAL

**Signal Pipeline:**
- `internal/pipeline/waterfall.go` — orchestrates: stale check → Layer 2 → Layer 3 → sizing
- Output: `Signal` struct with all scores, modifiers, and final verdict
- Telegram: bot posts formatted signal summary, awaits `/approve`

**Result:** `/import` PillarLab data → stale check catches price drift → bot runs Claude + market intel → outputs scored signals with position sizes → `/approve` to confirm.

---

### Phase 3: Paper Trading — "Fake It"

**Goal:** Simulate trades, track P&L, validate the full pipeline without real money.

**Paper Engine:**
- `internal/paper/portfolio.go` — In-memory portfolio tracker (start $1,000)
- `internal/paper/execution.go` — Simulated fills:
  - Tier A: fill at `best_bid + 0.001`
  - Tier B: fill at `best_bid` with simulated delay (random 1-12h)
  - Depth-aware: if order size > book depth, simulate partial fill
- `internal/paper/fees.go` — 2026 fee model:
  - Maker fee: 0 or negative (rebate of 20-25% of collected fees)
  - Taker fee (for reference/alerts): `Fee = C × 0.25 × (p × (1-p))²`
  - P&L calculation: `Profit = (Payout - EntryPrice) - Fee(maker)` using net profit after rebate

**Position Sizing:**
- `internal/sizing/kelly.go` — Fractional Kelly with maker rebate baked into win coefficient:
  - `b = (payout - entry_price - maker_fee) / entry_price` (maker_fee is negative = rebate, so b increases)
  - `kelly_pct = (p × (b + 1) - 1) / b` where p = win probability from PillarLab
  - `fractional_kelly = kelly_pct × 0.25`
  - This makes positioning more precise: maker rebate → larger b → slightly larger optimal bet
- `internal/sizing/formula.go` — Full formula:
  - `size = bankroll × fractional_kelly × clarity × liquidity_tier × theta × dca_level`
  - Constraints: min $5, max 15% bankroll, max 5 positions
  - **Tier B capital lockup limit: ≤40% of bankroll, resolution ≤30 days**

**Rebalancing:**
- `internal/monitor/rebalance.go` — Every 30 minutes for open positions:
  - Check price change since entry
  - If down >5%: run category divergence + correlation check
    - No news → DCA (buy more at 0.5× initial size)
    - News confirmed → hold, reassess
  - If up >10% (Tier A, >30 days to resolution): take partial profit
  - If up >10% (Tier A, <30 days): hold to resolution
  - Tier B: always hold to resolution
- DCA rules: max 3 entries, never >2× initial
- **Hard stop: never DCA if price <$0.15** — insight already priced in, event likely not happening

**Capital Lockup Monitor:**
- Track total capital allocated to Tier B positions
- Alert if approaching 40% lockup limit
- Block new Tier B trades if limit exceeded
- Telegram: `/status` shows "Tier B lockup: $180 / $200 (36%)"

**Trade Log:**
- SQLite: timestamp, market, side, price, size, fee(maker), clarity_score, theta, category, depth_flag, outcome
- Telegram: daily P&L summary, position updates

**Result:** Full pipeline running in paper mode. Daily Telegram reports showing simulated P&L with lockup tracking.

---

### Phase 4: Harden — "Don't Blow Up"

**Goal:** Add safety rails, realistic simulation, edge cases.

**Circuit Breakers:**
- `internal/risk/breakers.go`:
  - 3 consecutive losses → halt 24h
  - 5% daily drawdown → halt 24h
  - BTC/ETH >2% move in 5 min → cancel all resting orders
  - Telegram alert on every circuit breaker trigger

**Inventory Risk Management:**
- `internal/risk/lockup.go` — Global Capital Lockup Limit:
  - Max 40% of bankroll in Tier B markets
  - Tier B positions must resolve within 30 days
  - If all 5 position slots are Tier B with 3-month horizons → bot stops finding new trades
  - This rule ensures at least 60% of capital stays liquid for high-theta Tier A opportunities
  - Telegram: warn when approaching lockup limit, block when exceeded

**Nonce Management & Graceful Shutdown:**
- `internal/execution/nonce.go` — Thread-safe nonce manager for CLOB orders:
  - Polymarket CLOB requires a unique nonce for every order
  - With parallel goroutines (scan + rebalance + DCA), two orders could collide
  - Use `atomic.AddUint64` for single-instance bot (no Redis needed yet)
  - Each goroutine calls `nonce.Next()` which atomically increments and returns
  - **On graceful shutdown (SIGINT/SIGTERM):**
    - Persist current nonce to SQLite
    - Cancel all active Tier A limit orders (prevent fills when bot offline)
    - Tier B orders kept alive (hold-to-resolution strategy)
    - Log shutdown event with timestamp
  - On startup: reload nonce from DB, resume monitoring
  - Phase 6 (multi-instance): migrate to Redis INCR for distributed locking

**Rate Limit Backoff:**
- `internal/polymarket/ratelimit.go` — Exponential backoff with jitter:
  - Wrap all Gamma API and CLOB API calls with rate limiter
  - On HTTP 429 (Too Many Requests): wait `min(2^attempt × 100ms + jitter, 30s)`
  - Known limits: 60 orders/min, 100 public API/min, 300/10s for `/book`
  - Pre-emptive throttling: token bucket (e.g., `golang.org/x/time/rate`) to stay under limits proactively rather than hitting 429s
  - Log every 429 with endpoint + wait time for tuning

**Slippage Tolerance & Price Stalking Prevention:**
- `internal/execution/slippage.go` — Prevent chasing runaway prices:
  - When placing order at `best_bid + $0.001`, capture initial target price
  - Before submitting order, re-check current best_bid
  - If price moved >3% (configurable) from initial target → PAUSE, don't submit
  - Telegram alert: "⚠️ Price moved too fast: was $0.28, now $0.31 (+10.7%) — order paused"
  - Prevents: accidentally becoming taker, chasing momentum, overpaying on volatile markets
  - User can manually `/approve` again after reviewing new price, or skip the trade
  - This is a safety circuit breaker for fast-moving markets

**UMA Dispute Monitoring:**
- `internal/monitor/uma.go` — Check for active UMA disputes on held positions:
  - Poll UMA Oracle contract or Polymarket resolution status for open markets
  - If a dispute is filed on a market where we hold a position:
    - Immediate Telegram alert: "🚨 UMA DISPUTE filed on [market]. Outcome now unpredictable."
    - If Tier A: recommend immediate exit (sell position at current bid, take whatever you can get)
    - If Tier B: alert only (may not be able to exit due to low liquidity)
  - Track dispute outcomes over time to improve source trust scoring
  - Check frequency: every 30 minutes alongside rebalancing cycle

**Simulation Realism:**
- Order book walking: calculate actual average fill price across L2 depth
- Depth-constrained fills: if order > depth, simulate partial fill + time delay for remainder
- Latency simulation: add 50-100ms jitter to paper fills
- Partial fills: simulate orders only partially filling in low-liquidity markets

**Dead Market Detection:**
- Track last trade time per market
- Auto-cancel Tier B orders after 12h if unfilled
- Alert if Tier A market activity drops below threshold
- Detect markets that closed early (event resolved before endDate)

**Structured Logging:**
- JSON structured logs (zerolog or slog)
- Graceful shutdown on SIGINT/SIGTERM:
  - Persist nonce to DB
  - Cancel all Tier A resting orders (Tier B kept alive)
  - Close DB connections
  - Log shutdown event
- Telegram error alerts for critical failures

**Backtesting:**
- `internal/backtest/runner.go` — replay historical market data through the pipeline
- Use `golden_zone_research.json` and accumulated scan data as test corpus
- Validate: win rate, Sharpe ratio, max drawdown, lockup efficiency

**Result:** Hardened paper trading with realistic conditions, safe concurrent execution, and UMA dispute awareness. 2+ weeks of validated simulated P&L.

---

### Phase 5: Go Live — "Real Money, Small Size"

**Goal:** Place real orders on Polymarket CLOB. Start with $200-300, not full $500.

**Execution:**
- `internal/execution/clob.go` — Real CLOB order placement:
  - EIP-712 signing via `go-order-utils`
  - GTC limit orders (Tier A: best_bid + 0.001, Tier B: best_bid)
  - Pre-execution depth check: verify order size vs. book depth
  - Order lifecycle tracking: live → matched → confirmed / failed
  - Detect if order accidentally becomes taker (price moved) → alert immediately
- `internal/execution/cancel.go` — Cancel resting orders (individual + cancel_all for circuit breakers)
- Heartbeat: send keep-alive every 10 seconds to maintain orders

**Position Tracking:**
- Real-time P&L from CLOB trade history, accounting for maker rebates
- Track open orders, fills, cancellations
- Reconcile paper P&L vs. real P&L
- Monitor Tier B lockup percentage

**Telegram Enhancements:**
- `/trade` — manual trade override
- `/cancel <order_id>` — cancel specific order
- `/halt` — emergency stop, cancel all orders
- `/resume` — resume after halt
- `/lockup` — show Tier B capital allocation breakdown
- Real-time fill notifications
- Daily P&L summary with position breakdown + fee/rebate accounting

**Scaling:**
- Start: $200-300 for 2-4 weeks
- If profitable and validated: scale to full $1,000 and beyond
- Track metrics: win rate, avg net edge (after rebates), avg fill time, slippage, lockup efficiency

**Result:** Live trading with real (small) positions. Telegram pings on every fill.

---

### Phase 6: Scale & Optimize

**Goal:** Performance, infrastructure, and strategy improvements.

**Infrastructure:**
- WebSocket feeds: replace REST polling with `wss://ws-subscriptions-clob.polymarket.com/ws/market`
- In-memory LOB: local order book replica for instant slippage + depth calculation
- AWS deployment: eu-west-1 VPS, CPU core pinning, systemd service
- Redis: hot order book cache for sub-ms reads

**Strategy Enhancements:**
- Backtest Kelly fraction optimization (with maker rebate factored in)
- Refine Golden Zone bounds based on live data
- Expand correlation map with more market pairs
- Add Kalshi as secondary market source (more deal flow)
- Cross-platform arbitrage detection (same event, different prices)
- Adaptive theta: learn optimal time decay from historical results

**Monitoring:**
- Grafana dashboard or simple web UI
- Metrics: win rate, edge accuracy, fill rate, P&L curve, drawdown history, lockup utilization, maker rebate earnings
- Alerts: unusual market conditions, large position moves, system errors

---

## Telegram Command Reference

| Command | Description |
| :--- | :--- |
| `/scan` | Run market scan, display Golden Zone candidates with tier/theta/depth/activity |
| `/import <JSON>` | Parse PillarLab output, stale price check, run Claude + market intel, show signals |
| `/approve [N]` | Approve top N signals for execution |
| `/status` | Show open positions, P&L, balance, Tier B lockup % |
| `/lockup` | Detailed Tier B capital allocation breakdown |
| `/trade SIDE market_id` | Manual trade override |
| `/cancel <order_id>` | Cancel specific order |
| `/halt` | Emergency stop, cancel all orders |
| `/resume` | Resume trading after halt |
| `/pnl` | Daily/weekly P&L summary with fee/rebate breakdown |
| `/accuracy` | PillarLab prediction accuracy: win rate, calibration, edge analysis |
| `/help` | List all commands |

---

## Daily Workflow (20-30 minutes)

1. **Morning (automated):** Bot runs daily scan at 08:00 UTC → Telegram notification: "Found 18 Golden Zone candidates (12 Tier A, 6 Tier B). Tier B lockup: 22%/40%"
2. **Review (5 min):** Check bot output in Telegram, note top candidates with high theta + high liquidity + good depth
3. **PillarLab analysis (10-15 min):** Paste top 10-15 candidates into PillarLab with prompt template → get structured JSON output
4. **Import (2 min):** `/import <paste JSON>` → bot immediately checks for stale prices ("⚠️ 2 markets moved out of zone during research, skipped") → runs Claude trust/integrity scoring + market intel → outputs ranked signals with position sizes
5. **Approve (1 min):** Review signals, `/approve 3` to execute top 3
6. **Monitor (passive):** Bot sends Telegram updates on fills, price moves, rebalancing, DCA triggers, circuit breaker events, lockup warnings

---

## Expected Milestones

1. **Day 1-3:** Phase 1 — Scanner + Telegram bot with depth/activity/status checks
2. **Day 4-7:** Phase 2 — Waterfall engine with Claude scoring + integrity checks + stale price detection + market intelligence
3. **Day 8-12:** Phase 3 — Paper trading with full sizing (maker rebate-adjusted), rebalancing, lockup management
4. **Day 13-18:** Phase 4 — Hardened simulation, inventory risk limits, backtesting
5. **Day 19-24:** Phase 5 — Live trading with $200-300
6. **Day 25+:** Phase 6 — Scale, optimize, iterate

---

## Project File Structure

```
poly/
├── cmd/
│   └── poly/
│       └── main.go              # Entry point
├── internal/
│   ├── polymarket/
│   │   ├── gamma.go             # Gamma API client (market discovery)
│   │   ├── clob.go              # CLOB REST client (order book, orders)
│   │   └── ratelimit.go         # Exponential backoff + token bucket rate limiter
│   ├── scanner/
│   │   ├── filter.go            # Pre-filter pipeline (Golden Zone, liquidity, status, etc.)
│   │   ├── theta.go             # Time decay calculation
│   │   └── depth.go             # Order book depth analysis vs. position size
│   ├── analysis/
│   │   ├── claude.go            # Claude API (trust scoring, logic check)
│   │   ├── trust.go             # Source trust scoring engine
│   │   ├── integrity.go         # Market integrity checker (YES/NO asymmetry)
│   │   ├── category.go          # Category divergence detector
│   │   ├── correlation.go       # Correlated pairs engine
│   │   └── tavily.go            # Tavily fallback news check
│   ├── pipeline/
│   │   ├── waterfall.go         # Orchestrates Layer 1→2→3
│   │   └── stalecheck.go       # Re-check prices after PillarLab delay
│   ├── sizing/
│   │   ├── kelly.go             # Fractional Kelly (net edge after maker rebate)
│   │   └── formula.go           # Full position sizing formula
│   ├── paper/
│   │   ├── portfolio.go         # Paper portfolio tracker
│   │   ├── execution.go         # Simulated fills (depth-aware)
│   │   └── fees.go              # 2026 fee model (maker rebate accounting)
│   ├── execution/
│   │   ├── clob.go              # Real CLOB order placement
│   │   ├── cancel.go            # Order cancellation
│   │   └── nonce.go             # Thread-safe atomic nonce manager
│   ├── monitor/
│   │   ├── rebalance.go         # DCA + profit-taking logic
│   │   ├── uma.go               # UMA dispute monitoring for held positions
│   │   └── resolution.go        # Track market resolutions, update PillarLab accuracy log
│   ├── risk/
│   │   ├── breakers.go          # Circuit breakers
│   │   └── lockup.go            # Global Capital Lockup Limit (Tier B ≤40%)
│   ├── telegram/
│   │   └── bot.go               # Telegram bot + command router
│   ├── store/
│   │   ├── sqlite.go            # SQLite storage
│   │   └── predictions.go       # PillarLab prediction logging + accuracy tracking
│   └── backtest/
│       └── runner.go            # Backtesting engine
├── templates/
│   └── pillarlab_prompt.md      # PillarLab prompt template
├── config.yaml                  # Thresholds, tiers, parameters
├── .env                         # API keys (gitignored)
├── description.md               # Project brief
├── plan.md                      # This file
├── golden_zone_research.json    # Research dataset (22 markets)
├── golden_zone_analysis.md      # Market quality analysis
└── golden_zone_markets.csv      # Spreadsheet export
```
