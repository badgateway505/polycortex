# Golden Waterfall — System Architecture & Reference

**Last Updated:** 2026-03-26
**Current Phase:** 1 (Scanner + Filter Pipeline)

This document is the technical reference for how the system works. Use it to understand any module, debug issues, or onboard new components. If a module needs rework, start here.

---

## 1. Philosophy

**What we do:** Identify "Cognitive Divergence" — the gap between emotional retail sentiment and fundamental probability — on Polymarket's CLOB. We don't bet. We provide liquidity via maker limit orders and harvest price differences through a multi-layered analysis pipeline.

**How we make money:** Buy contracts in the Golden Zone ($0.20-$0.40) where Claude's deep analysis finds edge the market hasn't priced in. Hold to resolution (binary payout $0 or $1). At $0.30 entry, a win = 233% return.

**Core constraints:**
- $1,000 starting bankroll
- Quality over quantity: fewer, highly-analyzed trades
- 2-5 minute analysis window between discovery and verdict
- Target: <30 minutes daily human time

---

## 2. The Golden Zone ($0.20 - $0.40)

Statistical analysis of 90,000+ Polymarket addresses shows this is the only price segment with both a ~50% win rate AND convex payouts.

| Price | Win Rate | Payout on Win | Why |
|-------|----------|---------------|-----|
| <$0.20 | <15% | 400%+ | Lottery zone. Negative EV despite high payouts. |
| **$0.20-$0.40** | **~50%** | **150-400%** | **Sweet spot. Fee-efficient. Convex payouts.** |
| >$0.50 | >60% | <100% | Low payout. Fees eat edge. Overcrowded. |

Fee efficiency matters: 2026 taker fees peak at ~1.56%. In the Golden Zone, maker orders earn 20-25% rebates, making edge preservation critical. We never use taker orders unless edge is overwhelming (>15%).

---

## 3. The 4-Layer Filter Pipeline

This is the core of the system. We scan ~500 markets and progressively filter them through 4 modular layers. Each layer is independent, configurable via `config.yaml`, and logs its own rejections.

```
500 markets (Gamma API)
  |
  v
L1: Category Gate         ~490 pass    (kill noise at the door)
  |
  v
L2: Liveness Check        ~420 pass    (is this market alive?)
  |
  v
L3: Quality Gate          ~35 pass     (is it worth analyzing?)
  |
  v
[Fetch order books from CLOB API — expensive step, only for L3 survivors]
  |
  v
L4: Distribution Engine   ~12 Alpha, ~23 Shadow
  |
  v
Write ALL to signals DB (UPSERT on market_id)
  |
  v
Alpha shortlist → Revalidate → Send to Claude
```

**Cost savings:** Without filters, 500 markets x $0.02 Claude call = $10/scan. With filters, ~12 Alpha x $0.02 = $0.24/scan. **97.6% reduction.**

---

### Layer 1: Category Gate

**Purpose:** Kill noise. Only allow categories with a fundamental analytical basis — where outcomes are determined by verifiable real-world events, not social media posts or manipulation.

**Allowed:**
- Politics
- Crypto (Fundamental — protocol upgrades, ETF decisions, regulatory)
- Business
- Science
- Global Affairs
- Selected Sports (clear rule-based protocols: league standings, tournament results)
- Economics

**Excluded:**
- Pop Culture / Entertainment
- Meme coins / Questionable token prices
- Celebrity gossip ("Who marries whom")
- Anything where outcome depends on a single social media post
- Markets easily manipulated by small groups or insiders

**Config:** `config.yaml → category_gate.allowed_categories / excluded_categories`

**Code:** `internal/scanner/l1_category.go`

---

### Layer 2: Liveness Check

**Purpose:** Is this market alive and breathing? Filters out dead, inactive, and ghost markets.

**Checks:**
| Check | Threshold | Why |
|-------|-----------|-----|
| `active == true` | — | Market must be open for trading |
| `closed == false` | — | Not already resolved |
| `endDate > now` | — | Not expired |
| `liquidity > $1,000` | Configurable | At least some capital in the market |
| `volume_24h > $500` | Configurable | Market is actually being traded |

**Important architectural decision:** These filters run in the scanner pipeline (`l2_liveness.go`), NOT in the Gamma API fetch call (`gamma.go`). This keeps the API layer dumb and the filter logic modular/testable.

**Config:** `config.yaml → liveness.min_liquidity / min_volume_24h`

**Code:** `internal/scanner/l2_liveness.go`

---

### Layer 3: Quality Gate

**Purpose:** Is this market suitable for meaningful analysis? Checks time horizon and resolution source quality.

**Checks:**
| Check | Threshold | Why |
|-------|-----------|-----|
| Days to resolution | 3-30 days | <3d = noise/no time for trends. >30d = capital stagnation |
| Resolution source | Authoritative only | Must be binary, verifiable, manipulation-resistant |

**Resolution source validation:**
- **Good:** AP, Reuters, Bloomberg, ESPN, NBA.com, NFL.com, U.S. Government, Federal Reserve, CoinGecko, Coinbase
- **Bad:** "Social Media consensus", "Crowd determination", obscure blogs, vague media narratives, small organizations that could be biased/corrupt

**Config:** `config.yaml → quality.horizon_min_days / horizon_max_days / authoritative_sources`

**Code:** `internal/scanner/l3_quality.go`

---

### Between L3 and L4: Order Book Fetch

All markets surviving L1+L2+L3 get their **order book fetched from the CLOB API**. This is the expensive step (~1 API call per market, CLOB rate limit: 100/min), which is why we filter aggressively first.

**Data fetched per market:**
- Best bid and best ask prices
- Full L2 order book (all price levels and sizes)
- `clob_token_id` — the contract address needed to actually buy/sell this outcome later

**Code:** `internal/polymarket/clob.go`

---

### Layer 4: Distribution Engine

**Purpose:** Split L3 survivors into **Alpha** (shortlist for Claude) and **Shadow** (watchlist for backtesting). This is where the real pricing model lives.

#### VWAP — The Real Entry Price

We do NOT use `last_trade_price` to decide if a market is in the Golden Zone. Last trade can be hours old and misleading. Instead, we calculate **VWAP (Volume-Weighted Average Price)** — the actual average price we'd pay to enter a position based on the current order book.

**How VWAP works:**

```
Given: max_stake = 5% of balance = $50 (hard cap)
Given: order book ask side:
  $0.28 x 200 shares ($56 available)
  $0.29 x 150 shares ($43.50 available)
  $0.30 x 100 shares ($30 available)

To buy $50 worth:
  - Buy 178.6 shares at $0.28 = $50.00 (entire order fills at first level)
  - VWAP = $0.28

But if first level only has $30:
  - Buy 107.1 shares at $0.28 = $30.00
  - Buy 68.9 shares at $0.29 = $20.00
  - VWAP = $50.00 / 175.9 shares = $0.2841

Slippage = VWAP - best_ask = $0.2841 - $0.28 = $0.0041
```

The Golden Zone check applies to VWAP, not last price. If VWAP is $0.41 even though last trade was $0.38, the market fails the Golden Zone filter — because that's what we'd actually pay.

**Code:** `internal/scanner/vwap.go`

#### L4 Entry Criteria

All must pass for a market to be classified as **Alpha**:

| Check | Threshold | Rejection Reason | Why |
|-------|-----------|------------------|-----|
| VWAP at max stake | $0.20 - $0.40 | `OUTSIDE_GOLD_ZONE` | Must be in the Golden Zone at actual entry price |
| Total liquidity | > $5,000 | `LOW_LIQUIDITY_L4` | Enough capital for meaningful trading |
| True depth (±2% of best ask) | ≥ $250 | `THIN_DEPTH` | 5x max stake for safety margin |
| Spread percentage | ≤ 3% | `WIDE_SPREAD` | Spread must not eat our edge |

**True Depth explained:** We don't care about liquidity at $0.50 if we're buying at $0.28. True depth measures how much USD is available within ±2% of the best ask price. If there's only $40 within that zone and we want to stake $50, execution will be painful.

The $250 threshold (5x max stake) ensures there's enough cushion for our order plus other participants. If we're the biggest fish in a tiny pond, we move the market against ourselves.

**Spread explained:** `spread_pct = (best_ask - best_bid) / best_ask * 100`. A 3% spread on a $0.30 contract = $0.009. Wider than that and the bid-ask gap eats into our edge.

#### Routing

- **Pass ALL checks → Alpha** (shortlist). Gets sent to Claude for deep analysis.
- **Fail ANY check → Shadow** (watchlist). Tracked in DB but not analyzed by Claude. Used for backtesting: "What if we had traded this?" Enables filter calibration over time.

**Code:** `internal/scanner/l4_distribution.go`

---

## 4. Signal Storage

### The `signals` Table

Every market that passes L1+L2+L3 gets a row in the `signals` table — both Alpha and Shadow. This is the single source of truth for all market data.

**Key design decisions:**

1. **Single table, not two.** Alpha and Shadow live together, distinguished by `signal_type`. Simplifies queries, UPSERT logic, and resolution tracking.

2. **UPSERT on `market_id`.** One row per market. Rescanning updates price/volume/depth data without creating duplicates. `INSERT ... ON CONFLICT(market_id) DO UPDATE SET ...`

3. **`signal_type` is IMMUTABLE.** Once a market enters as Shadow, it stays Shadow forever — even if conditions improve on rescan. This is critical for backtesting integrity: "Shadow markets that later became tradeable" is exactly the data we need to calibrate filters.

4. **`updated_at` bumped on every UPSERT.** Shows when market data was last refreshed.

5. **`last_analyzed_at` prevents duplicate Claude calls.** If a market was analyzed <30 minutes ago, skip it on rescan. Don't pay for Claude twice.

### Signal Lifecycle

```
Created (L4 writes to DB)
  signal_type: alpha/shadow (IMMUTABLE)
  ai_insight: NULL
  trade_status: NOT_STARTED
  resolution_state: OPEN

Analyzed (Claude processes Alpha signals)
  ai_insight: PROCESSING → CONFIRMED or REJECTED
  edge_value: 0.12 (Claude's calculated edge)
  clarity: 0.85 (how verifiable the outcome is)
  raw_ai_response: full Claude response text (for debugging)
  last_analyzed_at: timestamp

Executed (order placed for CONFIRMED signals)
  trade_status: LIMIT_PLACED → FILLED or LIMIT_CANCELLED or FAILED
  rejection_reason: "Insufficient balance" (if FAILED)

Resolved (market outcome known)
  resolution_state: WIN / LOSE / VOID
```

### Fields Reference

**Identity:**
- `market_id` (UNIQUE, UPSERT key)
- `clob_token_id` — contract address for purchase, stored so we don't have to re-fetch

**Market metadata (from Gamma API):**
- `title`, `category`, `resolution_source` (name or URL), `end_date`

**Market data (from Gamma API):**
- `total_liquidity`, `volume_24h`, `last_trade_price`

**Order book data (from CLOB API):**
- `best_bid`, `best_ask`
- `spread_pct` — `(best_ask - best_bid) / best_ask * 100`
- `spread_absolute` — `best_ask - best_bid`
- `true_depth_2pct_usd` — total USD within ±2% of best ask
- `max_stake_size_usd` — 5% of balance (the hard cap, MVP: $50)
- `vwap_at_max_stake` — average fill price if we buy max stake
- `slippage_at_max_stake` — `best_ask - vwap_at_max_stake`

**Classification (from L4):**
- `signal_type` — `alpha` or `shadow` (IMMUTABLE)
- `is_alpha` — convenience boolean
- `rejection_reasons` — JSON array: `["OUTSIDE_GOLD_ZONE", "WIDE_SPREAD"]`
- `threshold_gap` — how far VWAP is from nearest Golden Zone boundary

**AI analysis (from Claude, populated later):**
- `ai_insight` — `NULL` / `PROCESSING` / `REJECTED` / `CONFIRMED`
- `clarity` — 0.0-1.0 (high = official press release; low = Twitter rumors)
- `edge_value` — Claude's calculated edge (e.g., 0.12 = 12%)
- `raw_ai_response` — full Claude response text for debugging
- `rejection_reason` — why Claude rejected, or why trade failed

**Execution (populated by execution engine):**
- `trade_status` — `NOT_STARTED` / `LIMIT_PLACED` / `FILLED` / `LIMIT_CANCELLED` / `FAILED`

**Resolution (populated when market resolves):**
- `resolution_state` — `OPEN` / `WIN` / `LOSE` / `VOID`

**Timestamps:**
- `created_at` — first time this market entered the DB
- `updated_at` — last UPSERT (market data refresh)
- `last_analyzed_at` — last time sent to Claude (prevents re-sends within 30min window)

---

## 5. AI Analysis (Claude)

Alpha signals get sent to Claude (Sonnet 4.5, Extended Thinking Mode) for deep analysis. Claude does NOT generate trading signals — it validates and scores signals identified by the filter pipeline.

### What Claude Does

**Resolution Trap Detection:** Audits contract wording for ambiguity.
- Red flags: "at the discretion of", "reports indicate", "generally acknowledged"
- Green flags: "solely determined by", "official results from", "certified by"

**Source Trust Scoring (4 levels):**
| Level | Score | Example |
|-------|-------|---------|
| L1 | 0.95 | AP, FIFA, SEC filings, NASA |
| L2 | 0.75 | Bloomberg, Reuters, government stats |
| L3 | 0.50 | "Any major news outlet", "widely reported" |
| L4 | 0.25 | Self-reporting with conflict of interest |

**Compound Condition Detection:** Markets requiring A AND B AND C for YES but only !A for NO have asymmetric probability. Claude detects this and applies a 0.5 position modifier.

**Conflict of Interest Check:** Resolution source financially interested in outcome → UMA dispute risk flag.

**Edge Calculation:** Claude estimates true probability, compares to market-implied probability (VWAP), and outputs the edge (positive or negative).

### Clarity Score

Clarity is Claude's confidence in the verifiability of the outcome. It directly scales position size.

| Clarity | Meaning | Example | Position Size Effect |
|---------|---------|---------|---------------------|
| 0.90+ | Official, binary, clear | Match result on league website, law published on gov portal | Full Kelly |
| 0.70-0.89 | Credible, mostly clear | Reuters report, institutional data | ~80% Kelly |
| 0.50-0.69 | Ambiguous or indirect | Analysis based on indirect signs, mixed media reports | ~50% Kelly |
| <0.50 | Unreliable | Twitter rumors, gossip, unverified claims | Skip or minimal |

**Raw response stored:** The full Claude response is saved in `raw_ai_response` for debugging when Claude returns malformed JSON or unexpected formatting.

### Anti-Duplicate Logic

Before sending to Claude:
1. Check `last_analyzed_at` — if within configurable window (default 30 min), skip
2. Set `ai_insight = 'PROCESSING'` — if bot restarts mid-analysis, this prevents re-sending and paying twice
3. On response: set `ai_insight = 'CONFIRMED'` or `'REJECTED'`, populate `edge_value`, `clarity`, `raw_ai_response`, update `last_analyzed_at`

---

## 6. Revalidation Before Claude

Markets can move between scan time and analysis time (2-5 minute window). Before spending money on Claude, we do a **full revalidation** — not just a price check, but a complete L4 re-run with fresh order book data.

**Steps:**
1. Fetch fresh order book for each Alpha signal
2. Recalculate VWAP, true depth, spread with current data
3. Re-run all L4 checks
4. If conditions still hold → proceed to Claude, update signals DB
5. If conditions degraded → log new rejection reasons, skip Claude
6. Check `last_analyzed_at` → skip if recently analyzed

**Why this matters:** If someone dumps shares between our scan and our analysis, VWAP could move from $0.35 to $0.45, taking the market out of the Golden Zone. Without revalidation, we'd waste $0.02 on Claude for a market we can't trade.

---

## 7. Execution Strategy: Smart Aggressive Maker

We are **Alpha Hunters**, not rebate farmers. The priority is capturing the edge Claude identified. Execution strategy is **edge-dependent**:

| Edge | Strategy | Time Window | Rationale |
|------|----------|-------------|-----------|
| >15% | Cross the spread | Immediate | Edge so large that ~2% taker fee is noise. Speed matters. |
| 8-15% | Post at best bid, then walk up | 60-90 seconds | Try for rebate, but don't miss the trade. |
| 5-8% | Post at best bid, wait | 3-5 minutes | Thin edge. If no passive fill, probably not worth it. |
| <5% | Skip or post-and-forget | — | Not enough edge to justify any risk of taker fees. |

**State machine:** `POST_BID → WAIT → WALK_UP → CROSS_SPREAD`

### Software Fuse: Max Buy Price

**Critical safety mechanism.** When crossing the spread, our limit order could execute at a terrible price if someone else hits the order book hard at the same moment (order book gets "emptied" between our price check and our order execution).

**Rule:** Bot NEVER buys above `vwap + max_cross_premium` (default 1%, configurable).

```
Crossing limit = min(cross_price, vwap * 1.01)
```

Worst case: our order doesn't fill. We never overpay. This is non-negotiable.

**Config:** `config.yaml → execution.max_cross_premium_pct: 1.0`

### Order Placement Details

- **BUY orders:** Start at `best_bid + $0.001` (queue jump for priority)
- **SELL orders:** Start at `best_ask - $0.001`
- **Queue jump rationale:** +$0.001 increases fill rate ~3x for negligible cost. Maker rebate more than covers it.
- **All orders are limit orders.** We never submit market orders. Even when "crossing the spread," we place a limit order at a specific price — just one that's high enough to match existing asks.

---

## 8. Position Sizing

### Formula

```
b = (Payout - EntryPrice - MakerFee) / EntryPrice
    where MakerFee is NEGATIVE (rebate) → b increases slightly

Kelly% = (p × (b + 1) - 1) / b
    where p = win probability (from Claude's analysis)

Fractional Kelly = Kelly% × 0.25   (quarter Kelly for safety)

Position Size = bankroll × fractional_kelly
    × clarity_score                 // Claude's confidence (0.0-1.0)
    × liquidity_modifier            // Tier A: 1.0, Tier B: 0.5
    × theta(days_to_resolution)     // ≤7d=1.0, ≤14d=0.9, ≤30d=0.75
    × dca_level                     // 1st: 1.0, 2nd: 0.5, 3rd: 0.25
```

### Constraints

| Constraint | Value | Why |
|-----------|-------|-----|
| **Hard cap per position** | **5% of bankroll ($50 on $1K)** | Prevents overexposure to any single market |
| Min position | $5 | Below this, fees destroy edge |
| Max concurrent positions | 5 | Diversification |
| Max Tier B lockup | 40% of bankroll | Keeps 60% liquid for Tier A opportunities |
| Tier B max resolution | 30 days | Prevents long-term capital freeze |

**5% is the HARD CAP, not the default.** Actual position size is determined by Kelly formula scaled by clarity. A high-clarity 15% edge might size at $45 (near cap). A low-clarity 6% edge might size at $12.

### Price Impact Check

Before placing an order, we walk the order book to check if our position size can actually be filled without moving the price significantly:

```
Effective fill price = VWAP for our exact order size
Price impact = (effective_fill - best_ask) / best_ask

If impact > 1%: reduce edge estimate by impact amount
If impact > 2%: reduce size or flag for manual approval
If insufficient depth: reject with clear message
```

---

## 9. Risk Management

### Circuit Breakers (Non-Negotiable)

| Trigger | Action | Resume |
|---------|--------|--------|
| 3 consecutive losses | Halt all trading for 24h | Manual `/resume` command |
| 5% daily drawdown | Halt all trading for 24h | Manual `/resume` command |
| BTC/ETH >2% move in 5 min | Cancel all resting orders | Auto after 1h or manual |

Once halted, the bot requires explicit `/resume` — no auto-resume.

### DCA Rules

| Condition | Action |
|-----------|--------|
| Price drops >5% AND no news (confirmed via market internals) | DCA at 50% of initial size |
| Max DCA entries | 3 per position |
| Total position | Never >2x initial allocation |
| **Price below $0.15** | **HARD STOP. Never DCA. The event is likely not happening.** |

### Profit Taking (Tier A Only)

Markets resolving in >30 days:
- +33% from entry → sell 30%
- +67% from entry → sell 30% more
- Hold remaining 40% to resolution (free ride)

Markets resolving in <30 days: hold 100% to resolution.
Tier B: always hold 100% to resolution (can't exit efficiently).

### UMA Dispute Handling

Check for active disputes every 30 minutes. If dispute filed on held position:
- Immediate Telegram alert
- Tier A: recommend immediate exit (sell at current bid)
- Tier B: alert only (may not be able to exit)
- Track dispute outcomes to improve trust scoring

### Graceful Shutdown

On SIGINT/SIGTERM:
1. Persist nonce to DB (atomic)
2. Cancel all Tier A resting orders
3. Keep Tier B orders alive (hold-to-resolution strategy)
4. Close DB connections

---

## 10. Shadow Mode & Calibration

Every Shadow signal is tracked to resolution. This creates a "what if" dataset:

- "If we had ignored the THIN_DEPTH filter, would we have profited?"
- "Are we leaving money on the table by filtering too aggressively?"
- "Is Alpha win rate actually better than Shadow win rate?"

### Calibration Queries

```sql
-- Alpha vs Shadow win rates
SELECT signal_type, COUNT(*),
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as wins
FROM signals WHERE resolution_state IN ('WIN', 'LOSE')
GROUP BY signal_type;

-- Which rejection reasons cost us money?
SELECT rejection_reasons, COUNT(*),
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as missed_wins
FROM signals WHERE signal_type = 'shadow' AND resolution_state IN ('WIN', 'LOSE')
GROUP BY rejection_reasons ORDER BY missed_wins DESC;

-- Near-miss analysis (markets just outside Golden Zone)
SELECT COUNT(*), AVG(threshold_gap), AVG(vwap_at_max_stake)
FROM signals WHERE signal_type = 'shadow'
AND rejection_reasons LIKE '%OUTSIDE_GOLD_ZONE%'
AND resolution_state = 'WIN' AND ABS(threshold_gap) < 0.02;
```

### When to Adjust Filters

Run `/calibrate` after 7 days, then again after 14 days. If suggestions are consistent:
- Filter rejecting profitable signals → loosen threshold
- Filter rejecting unprofitable signals → keep or tighten
- **Alpha win rate must be > Shadow win rate** — if not, L4 isn't adding value

---

## 11. Technical Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| Language | Go 1.25+ | Concurrency (goroutines), low-latency JSON |
| API - Markets | Polymarket Gamma API | Market discovery, metadata, prices |
| API - Order Book | Polymarket CLOB API | L2 book, depth, VWAP, order placement |
| API - AI | Claude Sonnet 4.5 (Extended Thinking) | Trust scoring, edge calculation |
| API - News | Tavily (Ultra-Fast, fallback only) | Breaking catalyst confirmation |
| Order Signing | `go-order-utils` (official) | EIP-712 signatures for CLOB |
| Interface | Telegram Bot | Commands, approvals, alerts |
| Database (Dev) | SQLite | Signals, scans, resolution tracking |
| Database (Prod) | Redis + SQLite | Order book cache (Redis), persistence (SQLite) |
| Hosting (Prod) | AWS Dublin (eu-west-1) | <1ms to London matching engine |

### Project Structure

```
poly/
├── cmd/poly/main.go                    # Entry point, config loading, CLI
├── internal/
│   ├── config/config.go                # YAML config loader
│   ├── polymarket/
│   │   ├── gamma.go                    # Gamma API client (market discovery)
│   │   ├── clob.go                     # CLOB API client (order books, orders)
│   │   └── ratelimit.go               # HTTP client with retry/backoff
│   ├── scanner/
│   │   ├── l1_category.go             # Layer 1: Category Gate
│   │   ├── l2_liveness.go            # Layer 2: Liveness Check
│   │   ├── l3_quality.go             # Layer 3: Quality Gate
│   │   ├── l4_distribution.go        # Layer 4: Distribution (VWAP, depth, routing)
│   │   ├── vwap.go                    # VWAP calculator (walks order book)
│   │   ├── depth.go                   # True depth analysis
│   │   └── pipeline.go               # Orchestrates L1→L2→L3→L4
│   ├── analysis/
│   │   ├── claude.go                  # Claude API client
│   │   ├── trust.go                   # Source trust scoring
│   │   └── integrity.go              # Market integrity checks
│   ├── pipeline/
│   │   ├── revalidate.go             # Full revalidation before Claude
│   │   ├── waterfall.go              # End-to-end pipeline orchestration
│   │   └── import.go                 # PillarLab JSON parser
│   ├── sizing/
│   │   ├── kelly.go                   # Kelly formula with maker rebate
│   │   ├── formula.go                # Full sizing with all modifiers
│   │   └── impact.go                 # Price impact calculator
│   ├── execution/
│   │   ├── clob.go                    # Real order placement
│   │   └── strategy.go              # Edge-dependent execution state machine
│   ├── store/
│   │   ├── sqlite.go                  # DB setup, migrations
│   │   └── signals.go                # Signals CRUD, UPSERT logic
│   ├── monitor/
│   │   ├── resolution.go             # Daily resolution check
│   │   └── analysis.go              # Performance analytics
│   ├── risk/
│   │   ├── breakers.go               # Circuit breakers
│   │   └── lockup.go                # Capital lockup limits
│   ├── telegram/
│   │   └── bot.go                    # Telegram bot framework
│   └── backtest/
│       └── runner.go                 # Replay scan data through pipeline
├── config.yaml                        # All configurable thresholds
├── .env                               # API keys (gitignored)
└── templates/
    └── pillarlab_prompt.md           # PillarLab prompt template
```

---

## 12. Configuration Quick Reference

All thresholds are in `config.yaml`. After editing, rebuild with `go build -o poly ./cmd/poly`.

```yaml
# Layer 1: Category Gate
category_gate:
  allowed_categories: [Politics, Crypto, Business, Science, ...]
  excluded_categories: [Pop Culture, Meme, Entertainment]

# Layer 2: Liveness Check
liveness:
  min_liquidity: 1000        # $1K minimum
  min_volume_24h: 500        # $500 minimum

# Layer 3: Quality Gate
quality:
  horizon_min_days: 3
  horizon_max_days: 30
  authoritative_sources: [AP, Reuters, Bloomberg, ...]

# Layer 4: Distribution Engine
distribution:
  golden_zone_min: 0.20
  golden_zone_max: 0.40
  min_liquidity: 5000        # $5K for Alpha
  min_true_depth_usd: 250    # $250 within ±2% of best ask
  max_spread_pct: 3.0
  default_stake_pct: 0.05    # 5% HARD CAP
  default_balance: 1000

# AI Analysis
analysis:
  min_reanalysis_interval: 30m

# Execution
execution:
  max_cross_premium_pct: 1.0  # Software fuse: never buy above VWAP + 1%

# Risk
risk:
  circuit_breaker_losses: 3
  circuit_breaker_drawdown: 0.05
  dca_threshold: 0.05
  dca_stop_price: 0.15       # Never DCA below $0.15
```

---

## 13. Telegram Commands

| Command | Phase | Purpose |
|---------|-------|---------|
| `/scan` | 1+ | Run full 4-layer pipeline, show Alpha/Shadow split |
| `/signals` | 2+ | Show Alpha signals (by ai_insight and trade_status) |
| `/shadow` | 2+ | Show Shadow signals and their outcomes |
| `/analyze` | 2+ | Revalidate + send Alpha to Claude |
| `/approve N` | 3+ | Approve top N signals for execution |
| `/status` | 1+ | Open positions, P&L, balance |
| `/calibrate` | 3+ | Suggest filter adjustments from Alpha vs Shadow data |
| `/accuracy` | 4+ | PillarLab/Claude prediction accuracy report |
| `/halt` | 5+ | Emergency stop: cancel all orders |
| `/resume` | 5+ | Resume after halt |
| `/help` | 1+ | List all commands |

---

## 14. Success Metrics

| Phase | Metric | Target |
|-------|--------|--------|
| Phase 1 | L1-L3 filters 500 → ~35 markets | <90 seconds |
| Phase 1 | L4 produces ~12 Alpha, ~23 Shadow | Correct routing |
| Phase 3 | Alpha win rate | >50% |
| Phase 3 | Alpha win rate > Shadow win rate | L4 adds value |
| Phase 5 | Maker orders | 100% (never taker accidentally) |
| Phase 5 | P&L after 1 week | ≥ break-even |
| Phase 6 | Bankroll growth | $1K → $2K → $5K+ |
| Ongoing | Daily human time | ≤30 minutes |
| Ongoing | Daily cost (Claude + Tavily) | <$2 |
