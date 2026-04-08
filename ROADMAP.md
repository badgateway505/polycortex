# Golden Waterfall — Development Roadmap & Status

**Last Updated:** March 27, 2026
**Current Phase:** Phase 1 (Scanner + Telegram Bot)
**Current Milestone:** M1.3 (Order Books)

**Principle:** Build the smallest testable thing at each step. Every milestone has a **validation gate** — if it fails, stop and fix before moving forward.

---

## Phase 1: Scanner + Telegram Bot (Day 1-3)

### Milestone 1.1: "Can we talk to Polymarket?" ✅ **COMPLETE**

**Status:** ✅ **COMPLETE** (March 24-25, 2026)

**Build:**
- `go mod init` + project skeleton
- `internal/polymarket/gamma.go` — minimal Gamma API client (just `GET /markets`)
- `internal/polymarket/ratelimit.go` — basic HTTP client with retry on 429
- `cmd/poly/main.go` — fetch 100 markets, print to stdout

**Test:**
```bash
go run ./cmd/poly
# Expected: prints 100 market titles + prices to terminal
```

**Validation gate:**
- [x] Can we fetch markets? (HTTP 200)
- [x] Does `outcomePrices` parse correctly? (it's a JSON string inside JSON — we saw this)
- [x] Do we get `active`, `closed`, `endDate`, `volume24hr`, `liquidity` fields?
- [x] What's the actual rate limit? (test with rapid requests) — 100 req/min confirmed

**Kill condition:** If Gamma API is down, broken, or returns unusable data → investigate before building anything else.

---

### Milestone 1.2a: "Layer Separation (L1/L2/L3)" ✅ **COMPLETE**

**Status:** ✅ **COMPLETE** (March 27, 2026)

**What was built:**
- [x] `internal/config/config.go` — YAML config loader with L1-L4 sections
- [x] `internal/scanner/l1_category.go` — Category Gate
- [x] `internal/scanner/l2_liveness.go` — Liveness Check
- [x] `internal/scanner/l3_quality.go` — Quality Gate
- [x] `internal/scanner/pipeline.go` — Orchestrates L1→L2→L3
- [x] `config.yaml` — L1-L3 sections configured

**Remaining (separate milestones):**
- ❌ VWAP calculator (`vwap.go`) — M1.3
- ❌ L4 Distribution Engine — M1.2b (requires M1.3)
- ❌ `signals` table schema — M1.2b
- ❌ Alpha/Shadow routing — M1.2b

**Philosophy:** Apply aggressive Pre-AI filters in 4 modular layers to save Claude API costs. Each layer is independent, testable, and configurable. Only send high-quality "Alpha" candidates to Claude for deep analysis. Track everything for backtesting.

**Data Flow:**
```
500 markets (Gamma API)
  → L1: Category Gate        → reject noise categories
  → L2: Liveness Check       → reject dead/inactive/ghost markets
  → L3: Quality Gate         → reject bad horizon / resolution source
  → [fetch order books for L3 survivors — CLOB API]
  → L4: Distribution Engine  → calculate VWAP, depth, spread
                              → route: Alpha (pass all) or Shadow (fail any)
  → Write ALL L3 survivors to signals table (UPSERT on market_id)
  → Alpha shortlist → revalidate → send to Claude
```

**Build:**
- [ ] `internal/scanner/l1_category.go` — Category Gate
- [ ] `internal/scanner/l2_liveness.go` — Liveness Check
- [ ] `internal/scanner/l3_quality.go` — Quality Gate
- [ ] `internal/scanner/l4_distribution.go` — Distribution Engine (requires CLOB data from M1.3)
- [ ] `internal/scanner/pipeline.go` — Orchestrates L1→L2→L3→L4, writes signals
- [ ] `internal/store/signals.go` — Signals DB with UPSERT logic

---

#### Layer 1: Category Gate — "Is this market worth looking at?"

Kills noise at the door. Only categories with a **fundamental analytical basis** pass through.

**Include:**
- Politics
- Crypto (Fundamental — protocol upgrades, ETF decisions, regulatory)
- Business
- Science
- Global Affairs
- Selected Sports (clear rule-based protocols: league standings, tournament results)
- Economics

**Exclude:**
- Pop Culture / Entertainment
- Meme coins / Questionable token prices
- "Who marries whom" / celebrity gossip
- Any market where outcome depends on a single social media post
- Markets easily manipulated by small groups or insiders

**Implementation:**
```go
// internal/scanner/l1_category.go
// Reads allowed/excluded lists from config.yaml
// Returns: []Market (passed), []Rejection (failed with reason EXCLUDED_CATEGORY)
```

---

#### Layer 2: Liveness Check — "Is this market alive and breathing?"

Filters out dead, inactive, and ghost markets. All liveness checks run here (not in the API fetch call) for modularity.

**Checks:**
- `active == true` (market is open, outcomes can still be traded)
- `closed == false`
- `endDate > now` (not expired)
- `liquidity > $1,000` (at least some capital in the market)
- `volume_24h > $500` (market has minimal activity, is "breathing")

**Implementation:**
```go
// internal/scanner/l2_liveness.go
// Moved OUT of gamma.go fetch — filters run in scanner pipeline, not API call
// Returns: []Market (passed), []Rejection (failed with reason DEAD_MARKET / LOW_VOLUME / LOW_LIQUIDITY)
```

**Note:** The Gamma API fetch (`gamma.go`) should return ALL non-closed markets without filtering. L2 handles all liveness logic.

---

#### Layer 3: Quality Gate — "Is this market analyzable?"

Validates that a market is suitable for meaningful analysis.

**Checks:**
- **Time horizon:** 3–30 days to resolution (enough for trend formation, not stale)
- **Resolution source validation:**
  - Only authoritative, verifiable sources
  - Prefer sources that are: binary and clearly defined, resistant to manipulation
  - Reject: vague media narratives, "Social Media consensus", "Crowd determination", small organizations that could be biased/corrupt/unreliable, obscure blogs

**Implementation:**
```go
// internal/scanner/l3_quality.go
// Returns: []Market (passed), []Rejection (failed with reason SHORT_HORIZON / LONG_HORIZON / WEAK_RESOLUTION_SOURCE)
```

---

#### Between L3 and L4: Order Book Fetch

All markets that survive L1+L2+L3 get their **order book fetched from CLOB API**.

This is the expensive step (1 API call per market), which is why we filter aggressively first.

**Data fetched per market:**
- Best bid, best ask
- Full L2 order book (for VWAP calculation)
- `clob_token_id` (contract address — stored in DB for later purchase)

---

#### Layer 4: Distribution Engine — "Alpha or Shadow?"

Splits surviving markets into two groups using **VWAP-based pricing** (not last trade price).

**VWAP Calculation:**
- Max stake = 5% of user balance — **hard cap** (MVP: max $50 on $1,000). Actual size determined later by Kelly × clarity.
- For VWAP/depth calculation, we use the max stake as worst-case scenario
- Walk the order book (ask side for buys) to calculate average fill price for that stake
- This is the **real entry price**, not the misleading last trade price

**Entry Criteria (all must pass for Alpha):**
| Check | Threshold | Rejection Reason |
|-------|-----------|------------------|
| VWAP at max stake falls in Golden Zone | $0.20 – $0.40 | `OUTSIDE_GOLD_ZONE` |
| Total liquidity | > $5,000 | `LOW_LIQUIDITY_L4` |
| True depth within ±2% of best ask | ≥ $250 (5× stake for safety) | `THIN_DEPTH` |
| Spread percentage | ≤ 3% | `WIDE_SPREAD` |

**Additional data recorded (for all markets, Alpha and Shadow):**
- `spread_absolute` = best_ask - best_bid
- `threshold_gap` = distance between VWAP and nearest golden zone boundary
- `slippage_at_max_stake` = best_ask - vwap_at_max_stake

**Routing:**
- Pass ALL checks → **Alpha** (shortlist, sent to Claude)
- Fail ANY check → **Shadow** (watchlist, tracked but not analyzed by Claude)

---

#### Signal Storage (After L4 — Single Table, UPSERT)

**ALL markets passing L1+L2+L3 are written to the `signals` table** after L4 classification.
One row per market, deduplicated via UPSERT on `market_id`.

**Schema: `signals`**
```sql
CREATE TABLE signals (
    -- Identity
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    market_id             TEXT NOT NULL UNIQUE,       -- UPSERT key
    clob_token_id         TEXT,                       -- contract address for purchase

    -- Market metadata (from Gamma API, L1-L3)
    title                 TEXT NOT NULL,
    category              TEXT,
    resolution_source     TEXT,                       -- name or URL
    end_date              TIMESTAMP,

    -- Market data (from Gamma API, L2)
    total_liquidity       REAL,
    volume_24h            REAL,
    last_trade_price      REAL,

    -- Order book data (from CLOB API, L4)
    best_bid              REAL,
    best_ask              REAL,
    spread_pct            REAL,                       -- (best_ask - best_bid) / best_ask * 100
    spread_absolute       REAL,                       -- best_ask - best_bid
    true_depth_2pct_usd   REAL,                       -- USD within 2% of best ask
    max_stake_size_usd    REAL,                       -- 5% of balance (MVP: $50)
    vwap_at_max_stake     REAL,                       -- avg fill price for max stake
    slippage_at_max_stake REAL,                       -- best_ask - vwap_at_max_stake

    -- Classification (L4 output)
    signal_type           TEXT NOT NULL DEFAULT 'shadow',  -- 'alpha' or 'shadow' (IMMUTABLE once set)
    is_alpha              BOOLEAN NOT NULL DEFAULT FALSE,
    rejection_reasons     TEXT,                       -- JSON array: ["OUTSIDE_GOLD_ZONE","WIDE_SPREAD"]
    threshold_gap         REAL,                       -- distance from golden zone boundary

    -- AI Analysis (populated later by Claude)
    ai_insight            TEXT DEFAULT NULL,          -- NULL / 'PROCESSING' / 'REJECTED' / 'CONFIRMED'
    clarity               REAL,                       -- 0.0-1.0, only meaningful for alpha
    edge_value            REAL,                       -- Claude's calculated edge (e.g., 0.12)
    raw_ai_response       TEXT,                       -- full raw Claude response for debugging bad formatting

    -- Execution (populated later by execution engine)
    trade_status          TEXT NOT NULL DEFAULT 'NOT_STARTED',
        -- NOT_STARTED: default
        -- LIMIT_PLACED: order in book, waiting for maker fill
        -- FILLED: in position
        -- LIMIT_CANCELLED: manually cancelled (e.g., price moved)
        -- FAILED: network error, API failure, insufficient balance

    -- Resolution (populated when market resolves)
    resolution_state      TEXT NOT NULL DEFAULT 'OPEN',
        -- OPEN: waiting for resolution
        -- WIN: profitable
        -- LOSE: loss
        -- VOID: market cancelled by Polymarket

    -- Rejection/failure reason (for ai_insight=REJECTED or trade_status=FAILED)
    rejection_reason      TEXT,                       -- e.g., "Edge < 5%", "Insufficient balance"

    -- Timestamps
    created_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,  -- bumped on UPSERT
    last_analyzed_at      TIMESTAMP                   -- when last sent to Claude (prevent re-sends)
);

CREATE INDEX idx_signals_market_id ON signals(market_id);
CREATE INDEX idx_signals_signal_type ON signals(signal_type);
CREATE INDEX idx_signals_ai_insight ON signals(ai_insight);
CREATE INDEX idx_signals_trade_status ON signals(trade_status);
CREATE INDEX idx_signals_resolution ON signals(resolution_state);
CREATE INDEX idx_signals_updated ON signals(updated_at);
```

**UPSERT Logic:**
```sql
INSERT INTO signals (market_id, clob_token_id, title, category, ...)
VALUES (?, ?, ?, ?, ...)
ON CONFLICT(market_id) DO UPDATE SET
    total_liquidity = excluded.total_liquidity,
    volume_24h = excluded.volume_24h,
    last_trade_price = excluded.last_trade_price,
    best_bid = excluded.best_bid,
    best_ask = excluded.best_ask,
    spread_pct = excluded.spread_pct,
    true_depth_2pct_usd = excluded.true_depth_2pct_usd,
    vwap_at_max_stake = excluded.vwap_at_max_stake,
    slippage_at_max_stake = excluded.slippage_at_max_stake,
    threshold_gap = excluded.threshold_gap,
    rejection_reasons = excluded.rejection_reasons,
    updated_at = CURRENT_TIMESTAMP;
    -- NOTE: signal_type is NOT updated (immutable)
    -- NOTE: ai_insight, trade_status, resolution_state are NOT overwritten by rescan
```

**Anti-duplicate analysis rule:**
- Before sending to Claude, check `last_analyzed_at`
- If `last_analyzed_at` is within a configurable window (e.g., 30 min), skip re-analysis
- Prevents paying for Claude twice if bot rescans frequently

---

**Configuration (`config.yaml`):**
```yaml
# Layer 1: Category Gate
category_gate:
  allowed_categories:
    - "Politics"
    - "Crypto"
    - "Business"
    - "Science"
    - "Global Affairs"
    - "Sports"
    - "Economics"
  excluded_categories:
    - "Pop Culture"
    - "Meme"
    - "Entertainment"
  # Note: unknown/empty categories pass through (filtered manually or by Claude later)

# Layer 2: Liveness Check
liveness:
  min_liquidity: 1000           # $1,000 minimum total liquidity
  min_volume_24h: 500           # $500 minimum daily volume

# Layer 3: Quality Gate
quality:
  horizon_min_days: 3           # At least 3 days to resolution
  horizon_max_days: 30          # No more than 30 days out
  authoritative_sources:        # Preferred resolution sources
    - "Associated Press"
    - "AP"
    - "Reuters"
    - "Bloomberg"
    - "ESPN"
    - "NBA.com"
    - "NFL.com"
    - "MLB.com"
    - "U.S. Government"
    - "Federal Reserve"
    - "Bureau of Labor Statistics"
    - "CoinMarketCap"
    - "CoinGecko"
    - "Binance"
    - "Coinbase"

# Layer 4: Distribution Engine
distribution:
  golden_zone_min: 0.20         # VWAP must be >= this
  golden_zone_max: 0.40         # VWAP must be <= this
  min_liquidity: 5000           # $5,000 minimum for Alpha
  min_true_depth_usd: 250       # $250 within ±2% of best ask (5× default stake)
  max_spread_pct: 3.0           # Maximum spread percentage
  default_stake_pct: 0.05       # 5% of balance = default stake
  default_balance: 1000         # MVP balance assumption ($1,000)

# Analysis timing
analysis:
  min_reanalysis_interval: 30m  # Don't re-send to Claude within this window

# Execution safety
execution:
  max_cross_premium_pct: 1.0    # SOFTWARE FUSE: never buy above VWAP + 1% when crossing spread
```

**Test:**
```bash
go run ./cmd/poly scan --limit 500
# Expected output:
# "Scanned 500 markets.
# L1 Category Gate:    490 passed, 10 rejected (EXCLUDED_CATEGORY)
# L2 Liveness Check:   420 passed, 70 rejected (DEAD_MARKET: 5, LOW_VOLUME: 42, LOW_LIQUIDITY: 23)
# L3 Quality Gate:      35 passed, 385 rejected (LONG_HORIZON: 370, SHORT_HORIZON: 8, WEAK_SOURCE: 7)
# [Fetching order books for 35 markets...]
# L4 Distribution:
#   Alpha (shortlist):  12 markets
#   Shadow (watchlist): 23 markets
# Signals written to DB (UPSERT): 35 total
# Ready: 12 Alpha candidates for Claude analysis"
```

**Validation gate (M1.2a — L1/L2/L3):**
- [x] L1 correctly filters noise categories
- [x] L2 filters are modular (not in gamma.go fetch call)
- [x] L2 correctly identifies ghost markets (volume < $500)
- [x] L3 horizon filter excludes <3 days and >30 days
- [x] L3 resolution source validation works
- [x] Liquidity numbers accurate (spot-checked against website)
- [x] Scanning 500 markets stays under rate limits (no 429s)

**Validation gate (M1.2b — L4, deferred until M1.3 complete):**
- [ ] L4 VWAP calculation matches manual order book walk
- [ ] L4 true depth check catches thin markets
- [ ] L4 spread check catches wide-spread markets
- [ ] Alpha/Shadow routing is correct
- [ ] Signals written to DB with all fields populated
- [ ] UPSERT updates price/volume without creating duplicates
- [ ] `signal_type` is immutable on UPSERT
- [ ] `updated_at` bumped on every UPSERT
- [ ] `last_analyzed_at` prevents re-sending to Claude within window
- [ ] `clob_token_id` stored for each market

**Cost Savings Analysis:**
- **Without filters:** 500 markets × $0.02 Claude call = $10/scan
- **With 4-layer pipeline:** ~12 Alpha markets × $0.02 = $0.24/scan
- **Savings:** 97.6% cost reduction
- **Bonus:** 23 Shadow markets tracked for free (backtesting gold)

**Kill condition:** If <5 Alpha candidates found → consider widening Golden Zone ($0.18-$0.42) or relaxing spread/depth thresholds.

---

### Milestone 1.3: "Can we read order books?" ✅ **COMPLETE**

**Status:** ✅ **COMPLETE** (March 27, 2026)

**Critical dependency:** L4 (Distribution Engine) in M1.2b requires order book data. This milestone provides the CLOB client and depth analysis that L4 calls.

**Build:**
- [x] `internal/polymarket/clob.go` — CLOB REST client (`GET /book`)
  - Returns full L2 order book with bids and asks
  - Includes `clob_token_id` (contract address) for each outcome
  - Rate limited to 100 req/min (reuses RateLimitedClient)
  - Automatic retry with exponential backoff on 429
  - `ParseBookSnapshot()` converts raw strings to parsed floats
- [x] `internal/scanner/depth.go` — Order book depth analysis
  - **Calculate "True Depth"** = sum of liquidity within ±2% of mid-price
  - Store both `totalLiquidity` (from Gamma) and `trueDepth` (from CLOB)
  - Calculate depth imbalance: `(bidDepth - askDepth) / totalDepth`
  - Detect "ghost liquidity" (high total, low true depth)
  - Detect "hype fade" (high volume, low depth)
  - `AssessLiquidityQuality()` combines Gamma + CLOB data for quality assessment
- [x] `internal/scanner/vwap.go` — VWAP calculator for L4
  - Walk the ask side of order book for given stake size
  - Calculate volume-weighted average price (real entry price)
  - Calculate slippage: `vwap - best_ask`
  - Returns fill quality assessment ("excellent", "good", "acceptable", "poor")
  - **Replaces "last trade price" as pricing model for Golden Zone filtering**
- [x] `cmd/poly/main.go` — Added `test-book` command for testing
  - Test CLOB client with real token IDs
  - Display book summary, VWAP, and depth analysis
  - Usage: `poly test-book --token <token_id> --stake <amount>`

**Test:**
```bash
go run ./cmd/poly --scan --depth
# Expected: each candidate now shows:
# "Arsenal CL [26%] Tier A | spread: $0.04 | TRUE depth: $2,340 (±2%) | total: $315K | imbalance: +0.12 | last trade: 2h ago"
#                                              ^^^^^^^^^^^^^^^^^^^^      ^^^^^^^^^^^^   ^^^^^^^^^^^^^^^
#                                              (actionable liquidity)    (Gamma metric) (buy pressure)
```

**Implementation Reference:**
```go
// internal/scanner/depth.go — Pro-tip architecture
type TrueDepth struct {
    MidPrice       float64
    BidDepthUSD    float64 // Sum of bids within ±2% of mid
    AskDepthUSD    float64 // Sum of asks within ±2% of mid
    TotalDepthUSD  float64 // Bid + Ask
    DepthImbalance float64 // (Bid - Ask) / Total (negative = sell pressure)
}

func CalculateTrueDepth(book OrderBook, tolerance float64) TrueDepth {
    mid := (book.BestBid + book.BestAsk) / 2.0
    lowerBound := mid * (1.0 - tolerance)
    upperBound := mid * (1.0 + tolerance)

    var bidDepth, askDepth float64

    // Walk bids (buy side) - only count orders near mid-price
    for _, level := range book.Bids {
        if level.Price >= lowerBound && level.Price <= upperBound {
            bidDepth += level.Price * level.Size
        }
    }

    // Walk asks (sell side)
    for _, level := range book.Asks {
        if level.Price >= lowerBound && level.Price <= upperBound {
            askDepth += level.Price * level.Size
        }
    }

    total := bidDepth + askDepth
    imbalance := 0.0
    if total > 0 {
        imbalance = (bidDepth - askDepth) / total
    }

    return TrueDepth{
        MidPrice:       mid,
        BidDepthUSD:    bidDepth,
        AskDepthUSD:    askDepth,
        TotalDepthUSD:  total,
        DepthImbalance: imbalance, // >0.2 = buy pressure, <-0.2 = sell pressure
    }
}
// Pro-tip: Pre-allocate slices if parsing 100+ books per scan
// Pro-tip: Cache book snapshots with 30s TTL (avoid hammering CLOB API)
// Pro-tip: For execution, recalculate depth JUST before order submission (freshness)
```

**Validation gate:**
- [x] Do we get full L2 book (not just best bid/ask)? — Implementation complete
- [x] True depth calculation excludes orders >2% from mid-price — Implemented in `CalculateTrueDepth()`
- [x] Markets with high `totalLiquidity` but low `trueDepth` are flagged — `AssessLiquidityQuality()` detects ghost liquidity
- [x] Depth imbalance calculation correct — `(bidDepth - askDepth) / totalDepth` implemented
- [x] VWAP calculation walks order book correctly — Implemented in `CalculateVWAP()`

**Testing pending (requires real token IDs from Gamma API):**
- [ ] Spread calculation spot-checked against website
- [ ] `/book` endpoint response time acceptable (<500ms per market)
- [ ] Can detect "dead" markets (via book timestamp or activity)

**Kill condition:** If CLOB `/book` endpoint returns stale data (known issue from research) → need to evaluate WebSocket earlier than planned.

---

### Milestone 1.4: "Theta + activity scoring works" (Day 2, ~1-2 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/scanner/theta.go` — Time decay calculation
- Activity scoring: last trade timestamp → active/slow/dying/dead
- **`internal/scanner/liquidity_quality.go`** — Distinguish volume from depth
  - **Calculate "Depth/Volume Ratio"** = `trueDepth / volume24h`
  - Flag markets where volume is high but depth is low ("hype fade" — was hot yesterday, dead now)
  - **Prefer markets with D/V ratio >0.05** (depth = 5%+ of daily volume = healthy execution)
  - **Skip markets with D/V <0.01** (illiquid despite volume — can't execute)
- Full pre-filter pipeline combining all filters
- Output: ranked list with all metadata

**Test:**
```bash
go run ./cmd/poly --scan --full
# Expected: fully scored candidates:
# "#1 Hungary PM [35%] Tier A | θ=0.90 | depth=$45K (D/V: 0.08 ✅) | spread=$0.03 | ACTIVE | 19d"
# "#2 Avalanche SC [20%] Tier A | θ=0.50 | depth=$12K (D/V: 0.06 ✅) | spread=$0.02 | ACTIVE | 98d"
# "#3 Hype Market [28%] Tier B | θ=0.90 | depth=$2K (D/V: 0.004 ⚠️ THIN) | spread=$0.12 | SLOW | 22d"
# ...
# "SUMMARY: 18 candidates (8 Tier A, 6 Tier B active, 2 Tier B flagged THIN, 2 skipped)"
```

**Why D/V Ratio Matters:**
- **High volume, low depth** = market was hot but liquidity dried up (hype fade)
- **High depth, low volume** = stable market with patient LPs (good for maker orders)
- **Both high** = active, liquid market (ideal)
- **Both low** = dead market (skip)

**Validation gate:**
- [ ] Theta values make sense (near-term markets get 0.9-1.0, long-term get 0.1-0.25)
- [ ] Activity scoring correctly identifies dead markets
- [ ] **Depth/Volume ratio correctly identifies "hype fade" markets**
- [ ] **Markets with D/V <0.02 are flagged (can execute but risky)**
- [ ] **Markets with D/V <0.01 are skipped (illiquid despite volume)**
- [ ] Ranking puts high-theta, high-liquidity, high D/V markets first
- [ ] Full scan completes in <60 seconds for 500 markets

**Kill condition:** None — this is pure logic, should work if inputs are correct.

---

### Milestone 1.5: "Telegram bot is alive" (Day 2-3, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/telegram/bot.go` — Bot framework with command router
- `/scan` command → triggers full scan pipeline, sends formatted results
- `/status` command → placeholder (no positions yet)
- `/help` command → lists available commands
- Config: `.env` with Telegram bot token

**Test:**
1. Create Telegram bot via @BotFather
2. Send `/scan` to bot
3. Bot responds with Golden Zone candidates (same output as Milestone 1.4, but in Telegram)

**Validation gate:**
- [ ] Bot connects to Telegram successfully
- [ ] `/scan` returns formatted results within 60 seconds
- [ ] Messages render correctly (not too long, no formatting breaks)
- [ ] Bot handles errors gracefully (API down, timeout)

**Kill condition:** None — Telegram bot API is well-documented and reliable.

---

### Milestone 1.6: "Save scan data for later use" (Day 3, ~1-2 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/store/sqlite.go` — SQLite setup with `signals` table (schema defined in M1.2)
- `internal/store/signals.go` — UPSERT logic for signals, query helpers
- The `signals` table is the **single source of truth** for all market data
  - Contains ALL markets that pass L1+L2+L3 (both Alpha and Shadow)
  - Full schema with 30+ fields: see M1.2 Signal Storage section
  - Key fields: market_id, clob_token_id, VWAP, depth, spread, signal_type, ai_insight, trade_status, resolution_state
  - UPSERT on `market_id` — one row per market, data updated on rescan
  - `signal_type` (alpha/shadow) is IMMUTABLE after first insert
  - `updated_at` bumped on every UPSERT
  - `last_analyzed_at` tracks when last sent to Claude (prevents duplicate analysis)
- Generate PillarLab prompt template from Alpha signals only

**Test:**
```bash
# Run full pipeline scan, check DB
sqlite3 poly.db "SELECT signal_type, COUNT(*) FROM signals GROUP BY signal_type"
# Expected:
# alpha|12
# shadow|23

# Check UPSERT works (rescan should update, not duplicate)
sqlite3 poly.db "SELECT COUNT(*) FROM signals"  # Before: 35
# Run scan again
sqlite3 poly.db "SELECT COUNT(*) FROM signals"  # After: still 35 (UPSERT, not INSERT)

# Check timestamps
sqlite3 poly.db "SELECT market_id, updated_at, last_analyzed_at FROM signals WHERE is_alpha = 1 LIMIT 3"

# Check prompt template output
go run ./cmd/poly --scan --pillarlab-prompt
# Expected: formatted prompt with only Alpha signals, ready to paste
```

**Validation gate:**
- [ ] Data persists across restarts
- [ ] UPSERT prevents duplicates (rescan doesn't create new rows)
- [ ] `signal_type` is immutable (shadow stays shadow even if market improves)
- [ ] `updated_at` changes on rescan, `created_at` stays original
- [ ] `last_analyzed_at` populated when sent to Claude
- [ ] Historical data queryable (can compare today vs. yesterday)
- [ ] Both Alpha and Shadow signals stored with full metadata
- [ ] PillarLab prompt template generates from Alpha signals only

---

### Phase 1 Complete Gate

**Before moving to Phase 2, ALL of these must be true:**
- [ ] 4-layer pipeline runs end-to-end: L1→L2→L3→(order book)→L4
- [ ] L1-L3 filter 500 markets down to ~30-50 quality candidates (cheap, Gamma-only)
- [ ] L4 splits survivors into Alpha (~10-15) and Shadow (~20-35)
- [ ] VWAP calculation matches manual order book walk
- [ ] Order book data (CLOB) is accurate and fresh — Requires M1.3
- [ ] Theta/depth scoring produces sensible rankings — Requires M1.4
- [ ] Telegram bot works reliably — Requires M1.5
- [ ] `signals` table populated via UPSERT, both Alpha + Shadow stored — Requires M1.6
- [ ] `clob_token_id` stored for each signal
- [x] Full L1-L3 scan completes in <90 seconds — DONE (<5 seconds for 500 markets)
- [x] No unhandled rate limit issues — DONE (verified with live scans)

---

## Phase 2: Waterfall Engine (Day 4-7)

**Overall Status:** ❌ NOT STARTED (waiting for Phase 1 completion)

### Milestone 2.1: "Claude can score a market" (Day 4, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/analysis/claude.go` — Claude API client (Extended Thinking)
- `internal/analysis/trust.go` — Source trust scoring
- `internal/analysis/integrity.go` — Market integrity checker
- Test with 3 known markets from our research:
  - Avalanche Stanley Cup (should be L1 trust, no flags)
  - Russia-Ukraine ceasefire (should be L3 trust, red flags)
  - A token/crypto market if available (should detect conflict of interest)

**Test:**
```bash
go run ./cmd/poly --analyze --market="avalanche-stanley-cup"
# Expected:
# Trust: L1 (0.95) — Official NHL results
# Red flags: none
# Green flags: "official results"
# Compound conditions: 0
# Conflict of interest: none
# Integrity: symmetric (YES=win, NO=not win)
# Clarity score: 0.95
# Position modifier: 1.0
```

**Validation gate:**
- [ ] Claude returns structured, parseable responses (not freeform text)
- [ ] Trust levels match our manual assessment
- [ ] Red/green flags detected correctly
- [ ] Compound conditions caught (test with a multi-condition market)
- [ ] Conflict of interest flagged where expected
- [ ] Response time acceptable (<10 seconds per market)
- [ ] Cost per analysis acceptable (~$0.01-0.05 per market with Extended Thinking)

**Kill condition:** If Claude API costs >$0.50 per market analysis → too expensive for 15-20 markets/day. Need to optimize prompt or switch to standard mode.

---

### Milestone 2.2: "PillarLab output can be parsed" (Day 4-5, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `templates/pillarlab_prompt.md` — Final prompt template
- `internal/pipeline/import.go` — JSON parser for PillarLab output
  - Handle messy formatting (markdown code blocks, extra text)
  - Extract: market, probability, edge, confidence, reasoning

**Test:**
1. Manually run PillarLab with our prompt template on 5 markets (use free tier)
2. Copy output → test parser

```bash
echo '<pillarlab output>' | go run ./cmd/poly --parse-import
# Expected: parsed 5 markets with structured data
```

**Validation gate:**
- [ ] PillarLab actually gives structured output with our template (THE BIG UNKNOWN)
- [ ] Parser handles messy formatting gracefully
- [ ] Probability estimates are in expected range
- [ ] We can assess PillarLab's edge estimates against current prices
- [ ] Credit usage per batch is acceptable (how many credits for 10-15 markets?)

**Kill condition:** If PillarLab ignores our structured output request or gives unusable data → fall back to Claude as Layer 1 or use Polyseer.

---

### Milestone 2.3: "Full revalidation before Claude" (Day 5, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Philosophy:** Before spending money on Claude analysis, **rescan all Alpha signals** to ensure conditions still hold. Markets can move between scan time and analysis time (2-5 minute window). This is not just a price check — it's a full L4 re-run.

**Build:**
- `internal/pipeline/revalidate.go` — Full revalidation pipeline:
  1. Fetch fresh order books for all Alpha signals
  2. Recalculate VWAP, true depth, spread with current book
  3. Compare against stored values in signals DB
  4. Re-run L4 checks: is VWAP still in Golden Zone? Depth still sufficient? Spread still OK?
  5. **If conditions still hold** → proceed to Claude, update signals DB with fresh data
  6. **If conditions degraded** → downgrade to Shadow, log reason, skip Claude analysis
  7. Check `last_analyzed_at` — if analyzed within configurable window (default 30min), skip re-analysis

**Trigger:** The "send to Claude" function/button must:
- Trigger a full rescan (not just price check)
- Compare updated vs stored data
- Proceed only if conditions still hold

**Test:**
```bash
# Simulate: Alpha signal where price moved out of Golden Zone
go run ./cmd/poly --revalidate
# Expected:
# "⚠️ Market X: VWAP was $0.28, now $0.42 — DEMOTED to Shadow (OUTSIDE_GOLD_ZONE)"
# "⚠️ Market Y: depth was $300, now $180 — DEMOTED to Shadow (THIN_DEPTH)"
# "✅ Market Z: VWAP $0.28 → $0.29, depth $400 → $380 — still Alpha, sending to Claude"
# "⏭️ Market W: analyzed 15 min ago — skipping (last_analyzed_at too recent)"
```

**Validation gate:**
- [ ] Correctly detects VWAP that moved out of Golden Zone
- [ ] Correctly detects depth that dropped below threshold
- [ ] Correctly detects spread that widened beyond 3%
- [ ] Demoted markets get `signal_type` changed? NO — `signal_type` is immutable. Instead, set `rejection_reasons` and skip Claude.
- [ ] `last_analyzed_at` check prevents duplicate Claude calls
- [ ] Fresh data written back to signals DB via UPSERT
- [ ] Handles edge cases (market closed during research, API error, order book empty)

---

### Milestone 2.4: "Market intelligence works" (Day 5-6, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/analysis/category.go` — Category divergence
- `internal/analysis/correlation.go` — Correlated pairs (start with manual correlation map)
- `internal/analysis/tavily.go` — Tavily fallback

**Test:**
```bash
# Test category divergence with real data
go run ./cmd/poly --category-check --market="arsenal-cl"
# Expected: "Arsenal: -3%. Sports avg: -1%. Divergence: -2%. NORMAL."

# Test Tavily fallback
go run ./cmd/poly --news-check --market="hungary-pm"
# Expected: "Tavily: 3 results. No breaking catalyst. CLEAR."
```

**Validation gate:**
- [ ] Category divergence calculation produces sensible results
- [ ] Tavily returns results and stays under budget ($0.008/search)
- [ ] Fallback chain works: internals first, Tavily only if inconclusive

---

### Milestone 2.5: "Full waterfall pipeline end-to-end" (Day 6-7, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/pipeline/waterfall.go` — Orchestrate everything
- Telegram `/import` command → stale check → Claude → market intel → signal output
- Signal ranking by `edge × clarity × theta`

**Test:**
1. Run `/scan` in Telegram → get candidates
2. Paste candidates into PillarLab (manual) → get JSON
3. Run `/import <JSON>` in Telegram → get scored signals

```
Expected Telegram output:
"📊 Imported 10 markets. Stale check: 1 skipped, 1 recalculated.
Signal #1: Avalanche SC [BUY YES] edge 5.2% | clarity 0.95 | θ 0.50 | size $38
Signal #2: Hungary PM [BUY YES] edge 4.1% | clarity 0.85 | θ 0.90 | size $42
Signal #3: Arsenal CL [BUY YES] edge 3.8% | clarity 0.90 | θ 0.50 | size $28
⚠️ Skipped: Russia ceasefire (clarity 0.35, too ambiguous)
Reply /approve to execute top 3"
```

**Validation gate:**
- [ ] Full pipeline runs without errors
- [ ] Signal ranking makes sense (high edge × clarity × theta first)
- [ ] Position sizes are reasonable ($20-80 range with $1,000 bankroll)
- [ ] Total pipeline time: <2 minutes from `/import` to signal output
- [ ] Skipped markets have clear reasons

### Phase 2 Complete Gate

**Before Phase 3:**
- [ ] Claude analysis produces consistent, useful trust scores
- [ ] PillarLab template works (or fallback plan activated)
- [ ] Full revalidation catches stale data before Claude analysis
- [ ] Market intelligence provides signal (even if basic)
- [ ] Full pipeline runs end-to-end via Telegram
- [ ] `signals` table populated with Alpha + Shadow markets, UPSERT working
- [ ] Cost per daily run: <$2 (Claude + Tavily)

---

## Phase 3: Paper Trading (Day 8-12)

**Overall Status:** ❌ NOT STARTED (waiting for Phase 2 completion)

### Milestone 3.1: "Kelly + formula produces correct sizes" (Day 8, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Position Sizing:** 5% of balance is the **hard cap** (MVP: max $50 on $1,000), NOT the default. Actual position size is calculated by Kelly formula, then scaled by Claude's **clarity score** (high clarity = official press release, league result, gov law → full Kelly; low clarity = Twitter rumors, gossip, indirect signs → reduced size). Kelly output is capped at `max_stake_pct` (5%) and floored at `min_size` ($5).

**Build:**
- `internal/sizing/kelly.go` — Kelly with maker rebate in `b`
- `internal/sizing/formula.go` — Full formula with all modifiers
- **`internal/sizing/impact.go`** — Price impact calculator
  - **Given target size (e.g., $50), calculate "Effective Fill Price" by walking the order book**
  - If order size exceeds available depth at best price, calculate weighted average fill price across multiple levels
  - **If effective fill price differs >1% from entry price, reduce edge estimate accordingly**
  - Formula: `adjusted_edge = base_edge - price_impact`
  - If impact >2%, either reduce size or flag for manual approval
- Unit tests with known inputs/outputs

**Test:**
```go
// Unit test examples:
// Entry $0.28, Payout $1.00, MakerFee -$0.003, WinProb 0.55
// b = (1.00 - 0.28 + 0.003) / 0.28 = 2.582
// Kelly% = (0.55 × 3.582 - 1) / 2.582 = 0.376
// Fractional = 0.376 × 0.25 = 0.094
// Base size = $1000 × 0.094 = $94
// After modifiers: × 0.95 (clarity) × 1.0 (Tier A) × 0.50 (θ 60d) × 1.0 (first entry)
// Final: $94 × 0.475 = $44.65

// PRICE IMPACT TEST:
// Target size: $50
// Order book depth at $0.28: $30, at $0.281: $40, at $0.282: $50
// Effective fill: ($30 × 0.28 + $20 × 0.281) / $50 = $0.2804
// Price impact: +0.14% → edge reduction: 5.0% → 4.86%
// If impact >2%, warn user or auto-reduce size
```

**Implementation Reference:**
```go
// internal/sizing/impact.go
func CalculatePriceImpact(book OrderBook, side string, sizeUSD float64) (effectivePrice, impact float64) {
    levels := book.Asks // For buy orders, walk the ask side
    if side == "SELL" {
        levels = book.Bids
    }

    remaining := sizeUSD
    totalCost := 0.0

    for _, level := range levels {
        availableUSD := level.Price * level.Size
        if availableUSD >= remaining {
            // This level fills the rest of our order
            shares := remaining / level.Price
            totalCost += shares * level.Price
            remaining = 0
            break
        } else {
            // Consume entire level, move to next
            totalCost += availableUSD
            remaining -= availableUSD
        }
    }

    if remaining > 0 {
        // Not enough liquidity — flag as high impact
        return 0, 999.0 // Signal: insufficient depth
    }

    effectivePrice = totalCost / sizeUSD
    basePrice := levels[0].Price
    impact = (effectivePrice - basePrice) / basePrice

    return effectivePrice, impact
}
```

**Validation gate:**
- [ ] Kelly formula outputs match manual calculation
- [ ] Maker rebate correctly increases `b` (compare with/without rebate)
- [ ] **Price impact calculation walks book correctly**
- [ ] **Edge adjusted for slippage when order size is significant**
- [ ] **If impact >2%, order size auto-reduced or flagged for approval**
- [ ] **If insufficient depth to fill order, reject with clear message**
- [ ] All modifiers apply correctly
- [ ] Constraints enforced: min $5, max 15% bankroll, max 5 positions
- [ ] Tier B lockup limit (40%) enforced

---

### Milestone 3.2: "Signal Tracker & Resolution Monitor" (Day 8-9, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Philosophy:** The unified `signals` table (created in M1.2/M1.6) already contains both Alpha and Shadow markets. This milestone adds **resolution tracking** and **performance analysis** on top of that table. No new tables needed — we use the existing `resolution_state`, `trade_status`, `ai_insight`, and `edge_value` fields.

**Build:**
- `internal/monitor/resolution.go` — Daily job to check if markets resolved
  - Poll Gamma API for each signal where `resolution_state = 'OPEN'`
  - If resolved: update `resolution_state` to WIN/LOSE/VOID
  - Calculate `theoretical_pnl` based on entry VWAP vs outcome
- `internal/monitor/analysis.go` — Performance analytics queries
- Telegram commands: `/signals`, `/analysis`, `/shadow`, `/calibrate`

**How it uses the `signals` table:**

| Field | Role in Tracking |
|-------|-----------------|
| `signal_type` | Alpha vs Shadow — **immutable**, enables "what if" comparison |
| `ai_insight` | NULL (shadow, not analyzed), PROCESSING, REJECTED, CONFIRMED |
| `trade_status` | NOT_STARTED → LIMIT_PLACED → FILLED / LIMIT_CANCELLED / FAILED |
| `resolution_state` | OPEN → WIN / LOSE / VOID |
| `edge_value` | Claude's edge — used to rank and analyze performance |
| `rejection_reason` | Why Claude rejected, or why trade failed |
| `updated_at` | Tracks freshness of market data |
| `last_analyzed_at` | Prevents duplicate Claude calls |

**Telegram Commands:**
```
/signals                  # Show all Alpha signals (by ai_insight and trade_status)
/analysis                 # Win rate, avg return for resolved signals
/shadow                   # Show Shadow signals and their outcomes (the "what if" report)
/calibrate                # Suggest filter adjustments based on Alpha vs Shadow performance
```

**Key Queries:**

```sql
-- Alpha performance (confirmed by Claude, tracked to resolution)
SELECT COUNT(*),
       SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as wins,
       AVG(edge_value) as avg_edge
FROM signals WHERE signal_type = 'alpha' AND resolution_state != 'OPEN';

-- Shadow "what if" analysis
SELECT COUNT(*),
       SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as would_have_won,
       rejection_reasons
FROM signals WHERE signal_type = 'shadow' AND resolution_state != 'OPEN'
GROUP BY rejection_reasons;

-- Are we leaving money on the table?
SELECT signal_type, AVG(edge_value), COUNT(*)
FROM signals WHERE resolution_state = 'WIN' GROUP BY signal_type;
```

**Test:**
```bash
# Day 1: Run scan → pipeline writes Alpha + Shadow to signals table
/scan
# "L1→L2→L3→L4 complete. 12 Alpha, 23 Shadow. Written to DB."

# Day 7: Check results
/analysis
# "Alpha: 12 total | 3 resolved | Wins: 2/3 (67%) | Avg edge: 8.2%"

/shadow
# "Shadow: 23 total | 8 resolved | Would have won: 3/8 (37%) | Top missed: 'Market X' (+12%)"
# "⚠️ THIN_DEPTH rejected 5 markets — 2 would have won. Consider lowering threshold?"

/calibrate
# "Suggestion: THIN_DEPTH threshold $250 → $200 (2 missed wins, avg +9% return)"
```

**Validation gate:**
- [ ] Resolution monitor correctly detects resolved markets
- [ ] WIN/LOSE/VOID states assigned correctly
- [ ] Shadow signals tracked to resolution (enables "what if" analysis)
- [ ] Performance queries return correct results
- [ ] UPSERT prevents duplicates on rescan
- [ ] `last_analyzed_at` prevents duplicate Claude calls
- [ ] Daily resolution check stays within rate limits (<1% of budget)
- [ ] Telegram commands return formatted, correct data

---

### Milestone 3.3: "Filter Calibration from Shadow Data" (Day 9-10, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/analysis/calibration.go` — Analyze shadow data, suggest adjustments
- Implement `/calibrate` command logic

**Analysis Queries (all from unified `signals` table):**

**Query 1: Alpha Performance by Category**
```sql
SELECT category, COUNT(*) as total,
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as wins,
    AVG(edge_value) * 100 as avg_edge_pct
FROM signals
WHERE signal_type = 'alpha' AND resolution_state IN ('WIN', 'LOSE')
GROUP BY category;
```

**Query 2: Shadow "What If" — Which Rejections Cost Us Money?**
```sql
SELECT rejection_reasons, COUNT(*) as times_rejected,
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as missed_wins
FROM signals
WHERE signal_type = 'shadow' AND resolution_state IN ('WIN', 'LOSE')
GROUP BY rejection_reasons
ORDER BY missed_wins DESC;
```

**Query 3: Alpha vs Shadow Win Rates**
```sql
SELECT signal_type, COUNT(*) as total,
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as wins,
    ROUND(100.0 * SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) / COUNT(*), 1) as win_rate_pct
FROM signals WHERE resolution_state IN ('WIN', 'LOSE')
GROUP BY signal_type;
```

**Query 4: Near-Miss Analysis (Markets Just Outside Golden Zone)**
```sql
SELECT COUNT(*) as total,
    AVG(vwap_at_max_stake) as avg_vwap,
    AVG(threshold_gap) as avg_gap,
    SUM(CASE WHEN resolution_state = 'WIN' THEN 1 ELSE 0 END) as would_have_won
FROM signals
WHERE signal_type = 'shadow'
AND rejection_reasons LIKE '%OUTSIDE_GOLD_ZONE%'
AND resolution_state IN ('WIN', 'LOSE')
AND ABS(threshold_gap) < 0.02;  -- Within 2 cents of boundary
```

**Test:**
```
/calibrate
# "🎯 FILTER CALIBRATION SUGGESTIONS
#
# Based on 14 days of shadow mode data:
#
# 1. ⚠️ LOW_CLARITY filter is too strict
#    - Rejecting signals with +5.1% avg return
#    - Current threshold: 0.70
#    - Suggested: 0.65
#    - Impact: +3-5 more trades/week
#
# 2. ✅ THIN_LIQUIDITY filter is working correctly
#    - Rejected signals lost -1.3% on average
#    - Keep current threshold: $5K
#
# 3. 🔍 Near-miss analysis: OUTSIDE_GOLDEN_ZONE
#    - 8 markets rejected within 2 cents of $0.20 boundary
#    - Avg return if taken: +4.8%
#    - Consider: Widen Golden Zone to $0.18-$0.42
#
# Apply changes? /calibrate apply"
```

**Validation gate:**
- [ ] Calibration queries run without errors
- [ ] Correctly identifies filters that are too strict (rejecting profitable signals)
- [ ] Correctly identifies filters that work (rejecting unprofitable signals)
- [ ] Near-miss analysis identifies opportunities just outside Golden Zone
- [ ] Suggestions are actionable (threshold adjustments, zone widening)

---

### Milestone 3.4: "Run Shadow Mode for 2 weeks" (Day 10-21)

**Status:** ❌ NOT STARTED

**Build:** Nothing new — run the full pipeline daily with shadow mode enabled

**Workflow:**
- **Day 1-7:** Collect data, minimal intervention
- **Day 7:** Run `/calibrate`, review suggestions (don't apply yet)
- **Day 8-14:** Continue collecting data
- **Day 14:** Run `/calibrate` again, compare to Day 7 (convergence check)
- **Day 15:** Apply calibration if suggestions are consistent

**Daily Routine (20-30 min):**
1. `/scan` → 4-layer pipeline writes Alpha + Shadow to `signals` table (UPSERT)
2. Review Alpha shortlist in Telegram
3. `/analyze` → Revalidate + send Alpha to Claude → updates `ai_insight` and `edge_value`
4. `/approve N` → Updates `trade_status` for top N signals
5. (Automated) Daily resolution check updates `resolution_state` for all signals

**Test:**
```
Day 1:  12 Alpha, 23 Shadow written to signals table
Day 3:  10 Alpha, 28 Shadow (UPSERT — no duplicates, just updated data)
Day 7:  /calibrate → "THIN_DEPTH too strict: 3 Shadow markets with THIN_DEPTH won (+9% avg)"
Day 10: 14 Alpha, 25 Shadow
Day 14: /calibrate → "THIN_DEPTH still flagged: consistent pattern" ← Confirm adjustment
Day 15: Lower true_depth threshold $250 → $200 in config.yaml
Day 21: /calibrate → "THIN_DEPTH now neutral" ← Fixed
```

**Validation gate:**
- [ ] Pipeline works reliably over 14 days
- [ ] No crashes, no data corruption
- [ ] `signals` table has 50+ unique markets (both Alpha and Shadow)
- [ ] Alpha signals have 10+ resolved markets (enough for analysis)
- [ ] Shadow signals tracked to resolution (enables "what if" comparison)
- [ ] `/calibrate` produces consistent suggestions (Day 7 ≈ Day 14)
- [ ] Time investment is actually 20-30 min/day as planned
- [ ] **Win rate sanity check:** Alpha signals should have >50% win rate

**Kill condition:** If every signal gets filtered to Shadow (too conservative) → loosen L4 thresholds. If nothing gets filtered (too loose) → tighten.

### Phase 3 Complete Gate

**Before Phase 4:**
- [ ] 14 days of data collected in unified `signals` table
- [ ] 50+ unique signals (Alpha + Shadow combined)
- [ ] Alpha signals have 10+ resolved markets (statistical significance)
- [ ] Shadow signals tracked to resolution (100+ for "what if" analysis)
- [ ] Win rate for Alpha signals >50%
- [ ] Avg edge_value for winning Alpha signals >0% (positive expected value)
- [ ] Alpha win rate > Shadow win rate (proves L4 adds value)
- [ ] `/calibrate` produces consistent suggestions (Day 7 ≈ Day 14)
- [ ] At least 1 L4 threshold adjusted based on calibration data
- [ ] Daily workflow is <30 minutes
- [ ] No critical bugs

---

## Phase 4: Harden (Day 13-18)

**Overall Status:** ❌ NOT STARTED (waiting for Phase 3 completion)

### Milestone 4.1: "Circuit breakers work" (Day 13, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/risk/breakers.go` — All circuit breaker rules
- `internal/risk/lockup.go` — Capital lockup limits

**Test:**
```
# Simulate 3 consecutive losses → should halt
# Simulate 5% drawdown → should halt
# Simulate Tier B lockup at 42% → should block new Tier B trades
# Simulate BTC flash crash → should cancel all orders (paper mode: alert)
```

---

### Milestone 4.2: "Nonce management is thread-safe & graceful shutdown works" (Day 13-14, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/execution/nonce.go` — Atomic nonce counter with persistence
- Graceful shutdown handler:
  - On SIGINT/SIGTERM: persist nonce, cancel all Tier A orders, close connections
  - Tier B orders kept alive (hold-to-resolution strategy)
- Stress test with concurrent goroutines

**Test:**
```go
// Launch 100 goroutines, each requesting a nonce
// Verify: all 100 nonces are unique, no duplicates, no gaps

// Test graceful shutdown:
// 1. Place Tier A and Tier B orders
// 2. Send SIGINT
// 3. Verify: nonce persisted, Tier A orders canceled, Tier B orders still live
// 4. Restart bot
// 5. Verify: nonce reloaded correctly, no duplicate nonces on next order
```

**Validation gate:**
- [ ] Nonce counter is thread-safe (no duplicates under concurrent load)
- [ ] Nonce persists on shutdown and reloads on startup
- [ ] Tier A orders canceled on shutdown (Tier B kept alive)
- [ ] No order collisions after restart

---

### Milestone 4.3: "Slippage tolerance prevents price stalking" (Day 14, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/execution/slippage.go` — Price movement guard
- Before submitting order at `best_bid + $0.001`:
  - Capture initial target price
  - Re-check current best_bid just before submission
  - If moved >3% → pause and alert, don't submit
  - Prevents accidentally becoming taker or chasing runaway prices

**Test:**
```
# Simulate price scenarios:
# 1. Price stable: $0.28 → $0.281 → should SUBMIT
# 2. Price moved 2%: $0.28 → $0.286 → should SUBMIT (within tolerance)
# 3. Price jumped 5%: $0.28 → $0.294 → should PAUSE and ALERT
# 4. Price jumped 15%: $0.28 → $0.322 → should PAUSE and ALERT

# Expected Telegram alert:
# "⚠️ Price moved too fast: was $0.28, now $0.322 (+15%) — order paused. /approve again to retry or skip."
```

**Validation gate:**
- [ ] Slippage tolerance correctly detects fast price moves
- [ ] Orders paused (not submitted) when price exceeds threshold
- [ ] User can manually retry after reviewing new price
- [ ] Never accidentally becomes taker due to price stalking

---

### Milestone 4.4: "UMA dispute monitoring" (Day 14-15, ~3-4 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/monitor/uma.go` — Poll for dispute status
- Research: which Polymarket API or UMA contract endpoint gives dispute status?

**Test:**
- Find a historical market that had a UMA dispute
- Verify our monitor would have detected it
- Test Telegram alert formatting

**Kill condition:** If no API exists for UMA dispute status → implement as a manual Telegram command (`/dispute market_id`) rather than automated monitoring. Still valuable as a workflow, just human-triggered.

---

### Milestone 4.5: "Backtesting with historical data" (Day 15-17, ~4-6 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/backtest/runner.go` — Replay scan data through pipeline
- Use accumulated 5-10 days of scan data from Phase 3

**Test:**
```bash
go run ./cmd/poly --backtest --from=2026-03-24 --to=2026-04-05
# Expected:
# "Backtested 12 days, 47 signals, 23 trades
# Win rate: 52% | Net P&L: +$85 | Max drawdown: -$42
# Sharpe: 1.2 | Avg edge: 4.1% | Avg fill time: 2.3h"
```

**Validation gate:**
- [ ] Win rate > 45% (below this, strategy needs rethinking)
- [ ] No catastrophic drawdown scenarios
- [ ] Kelly sizing prevents oversized losses
- [ ] Circuit breakers would have triggered at right times

**Kill condition:** If backtest win rate < 40% consistently → strategy fundamentals are wrong. Go back to signal generation (PillarLab quality, Claude scoring thresholds).

---

### Milestone 4.6: "PillarLab accuracy tracking" (Day 17-18, ~2-3 hours)

**Status:** ❌ NOT STARTED

**Build:**
- `internal/store/predictions.go` — Log every PillarLab prediction
- `internal/monitor/resolution.go` — Poll resolved markets, match against predictions
- Telegram `/accuracy` command

**What gets logged on every `/import`:**
- market_id, question, pillarlab_probability, pillarlab_edge, market_price_at_import, our_side, confidence, timestamp

**What gets logged on resolution:**
- actual_outcome (YES/NO), resolution_price, was_pillarlab_correct, actual_pnl

**Test:**
```
/accuracy
# "PillarLab Accuracy Report (last 30 days):
# Predictions: 47 | Resolved: 31 | Pending: 16
# Win rate: 58.1% (18/31)  ← compare to their claimed 64%
# Avg predicted edge: 5.3% | Avg actual edge: 2.8%
# Calibration: when PillarLab says 70%, actual win rate is 62%
# Best category: Sports (67%) | Worst: Politics (51%)"
```

**Validation gate:**
- [ ] Every PillarLab prediction is logged (none missed)
- [ ] Resolution outcomes matched correctly
- [ ] Accuracy report generates without errors

**Why this matters:** After 50-100 predictions, you'll know if PillarLab is worth $29/mo or if you should switch to Claude/Polyseer as Layer 1. This is the data that tells you whether your signal source is real.

### Phase 4 Complete Gate

**Before going live:**
- [ ] All circuit breakers tested and working
- [ ] Nonce management proven thread-safe with graceful shutdown
- [ ] Tier A orders auto-cancel on bot shutdown (tested)
- [ ] Slippage tolerance prevents price stalking (tested with simulated price jumps)
- [ ] UMA monitoring in place (automated or manual)
- [ ] 2+ weeks of paper trading data
- [ ] Backtesting shows positive expected value
- [ ] Rate limit backoff tested under load
- [ ] No unhandled crashes in 5+ days of running

---

## Phase 5: Go Live (Day 19-24)

**Overall Status:** ❌ NOT STARTED (waiting for Phase 4 completion)

### Milestone 5.1: "Can we sign and submit a real order?" (Day 19, ~4-6 hours)

**Status:** ❌ NOT STARTED

**Execution Philosophy: "Smart Aggressive Maker"**

We are **Alpha Hunters**, not rebate farmers. The priority is capturing the edge Claude identified, not saving $0.50 in fees. Execution strategy is **edge-dependent**:

| Edge Size | Strategy | Rationale |
|-----------|----------|-----------|
| **>15% edge** | Cross the spread immediately | Edge so large taker fees (~2%) are noise. Speed matters. |
| **8-15% edge** | Post at best bid for 60-90s, then walk up | Try for rebate, but don't let it expire. |
| **5-8% edge** | Post at best bid, wait up to 3-5 min | Thin edge. If no passive fill, probably not worth it. |
| **<5% edge** | Skip or post-and-forget | Not enough edge for taker fees. Get great entry or move on. |

**State machine:** `POST_BID → WAIT → WALK_UP → CROSS_SPREAD`

**Build:**
- `internal/execution/clob.go` — Real order placement with `go-order-utils`
- `internal/execution/strategy.go` — Edge-dependent execution state machine
- **Order placement logic:**
  - **For BUY orders:** Start at `best_bid + $0.001` (queue jump for priority)
  - **For SELL orders:** Start at `best_ask - $0.001`
  - **Escalation:** If not filled within time window, walk up price or cross spread (based on edge tier)
  - **SOFTWARE FUSE on CROSS_SPREAD:** Bot must NEVER buy above `vwap + max_cross_premium` (default 1%, configurable). If someone hits the order book hard at the moment we cross, our limit order could execute at a terrible price. The fuse ensures our crossing limit is set at `min(cross_price, vwap * 1.01)` — worst case we don't fill, we never overpay.
  - **Maker rebate (-0.3%)** already included in Kelly `b` calculation (from Milestone 3.1)
  - **Never use market orders** — start as maker, only cross spread deliberately when edge justifies it
  - Track fill rate per strategy tier to tune timeouts over time
- Test with absolute minimum size on a high-liquidity market

**Test:**
1. Place a single limit order (minimum size, best_bid - $0.05 so it won't fill)
2. Verify order appears in CLOB
3. Cancel the order
4. Verify cancellation

**Validation gate:**
- [ ] EIP-712 signing works with `go-order-utils`
- [ ] Order accepted by CLOB (no signature errors)
- [ ] Order visible in book
- [ ] **Order placed at `best_bid + $0.001` (not best_bid, not best_bid + $0.01)**
- [ ] **Order is maker (not taker) — verify fee is negative (rebate)**
- [ ] Cancellation works
- [ ] Heartbeat keeps order alive

**Kill condition:** If `go-order-utils` doesn't work with current contracts → need to debug signing or use a different signing approach.

---

### Milestone 5.2: "First real trade (tiny)" (Day 19-20)

**Status:** ❌ NOT STARTED

**Build:** Connect execution to pipeline

**Test:**
1. Run full pipeline → get signal
2. `/approve 1` with live mode enabled
3. Place real order: $5-10 (absolute minimum)
4. Monitor: did it fill? At what price?

**Validation gate:**
- [ ] Order fills at expected price (maker, not taker)
- [ ] Fee/rebate matches expectations
- [ ] Position tracking accurate
- [ ] P&L calculation matches CLOB trade history

---

### Milestone 5.3: "Run live for 1 week at $200-300" (Day 20-24)

**Status:** ❌ NOT STARTED

**Scale up gradually:**
- Day 1-2: $5-10 per trade (testing)
- Day 3-4: $20-30 per trade (small)
- Day 5-7: Full Kelly sizing on $200-300 bankroll

**Validation gate:**
- [ ] No execution errors in 5+ days
- [ ] Maker orders consistently (never accidentally taker)
- [ ] Fills within expected time
- [ ] Reconcile: paper P&L vs. real P&L (should be close)
- [ ] Telegram alerts working for all events
- [ ] Circuit breakers fire correctly if needed

### Phase 5 Complete Gate

**Before scaling:**
- [ ] 1+ week of live trading without issues
- [ ] Execution is reliable and maker-only
- [ ] P&L is positive or break-even (not hemorrhaging)
- [ ] All monitoring and alerts working
- [ ] Comfortable scaling to full $1,000

---

## Phase 6: Scale & Optimize (Day 25+)

**Overall Status:** ❌ NOT STARTED (waiting for Phase 5 completion)

### Milestone 6.1: Scale to $1,000
**Status:** ❌ NOT STARTED

### Milestone 6.2: WebSocket feeds (replace REST polling)
**Status:** ❌ NOT STARTED

### Milestone 6.3: AWS deployment
**Status:** ❌ NOT STARTED

### Milestone 6.4: Redis for order book cache
**Status:** ❌ NOT STARTED

### Milestone 6.5: Kalshi integration (more deal flow)
**Status:** ❌ NOT STARTED

### Milestone 6.6: Strategy tuning from live data
**Status:** ❌ NOT STARTED

*(Detailed milestones defined after Phase 5 validation, based on what we learn from live trading.)*

---

## Critical Kill Conditions Summary

| Phase | Kill Condition | Fallback |
| :--- | :--- | :--- |
| 1.1 | Gamma API broken/unusable | Wait, retry, check docs |
| 1.2 | <5 Golden Zone markets | Widen range to 0.15-0.45 |
| 1.3 | CLOB `/book` returns stale data | Move to WebSocket earlier |
| 2.1 | Claude costs >$0.50/market | Optimize prompt, use standard mode |
| 2.2 | PillarLab won't give structured output | Claude as Layer 1, or Polyseer |
| 3.4 | All signals filtered out | Loosen thresholds |
| 4.4 | Backtest win rate <40% | Rethink signal generation |
| 5.1 | `go-order-utils` signing fails | Debug or alternative signing |

---

## Time Investment Summary

| Phase | Duration | Your Time (daily) | What You Learn |
| :--- | :--- | :--- | :--- |
| Phase 1 | 3 days | ~1h coding sessions | "Can we see the market?" |
| Phase 2 | 4 days | ~1h coding + 15min PillarLab test | "Can we identify good trades?" |
| Phase 3 | 5 days | ~30min daily monitoring | "Does our strategy make sense?" |
| Phase 4 | 6 days | ~30min monitoring + review | "Is it safe to go live?" |
| Phase 5 | 6 days | ~20-30min daily workflow | "Does it make real money?" |
| Phase 6 | Ongoing | ~20-30min daily workflow | "How do we make more?" |
