# CLAUDE.md — Development Guidelines for "Golden Waterfall"

## Project Overview

**Golden Waterfall** is a hybrid human-AI trading system for Polymarket CLOB (Central Limit Order Book). It combines specialized AI signal generation with automated execution, risk management, and position monitoring, targeting the "Golden Zone" ($0.20-$0.40 price range) for optimal risk/reward.

**Core Philosophy**: Maker-only orders, multi-layer analysis pipeline, fail-safe risk management, gradual validation from paper to live trading.

## Critical Development Principles

### 1. Safety First — Never Risk Real Money Prematurely

- **ALWAYS start with paper trading** for any new feature that affects execution or position sizing
- Test circuit breakers and risk limits with simulated scenarios before going live
- When in doubt, add a confirmation gate rather than automating destructively
- Prefer conservative defaults: it's better to miss a trade than to blow up the account

### 2. Maker-Only Execution is Sacred

- **NEVER use market orders** — all trades must be limit orders (maker)
- If an order accidentally becomes taker (price moves), **IMMEDIATELY alert** via Telegram
- The fee structure in the Golden Zone makes taker fees edge-destroying — this is not negotiable
- Every execution path must verify: "Am I placing a maker order at the correct price level?"

### 3. Fail-Safe Architecture

- Every external API call (Polymarket, Claude, Tavily) must have:
  - Exponential backoff with jitter on rate limits (429)
  - Timeout handling (10s for most, 30s for Claude Extended Thinking)
  - Graceful degradation (log error, alert user, continue with reduced functionality)
- Database writes must be atomic (use transactions for multi-table updates)
- Nonce management must be thread-safe (atomic operations, no race conditions)
- Graceful shutdown on SIGINT/SIGTERM (persist nonce, close DB, cancel orders)

### 4. Validation Gates at Every Phase

- Follow the roadmap's validation gates strictly:
  - Phase 1: Scanner must find 15+ Golden Zone markets daily before moving to Phase 2
  - Phase 2: Claude analysis must produce consistent scores before paper trading
  - Phase 3: 3-5 days of paper trading before hardening
  - Phase 4: 2+ weeks of validated simulation before going live
  - Phase 5: 1+ week at small size before scaling
- If a validation gate fails, **STOP** and fix the issue — do not proceed to next phase

### 5. Incremental Complexity

- Build the smallest testable vertical slice first
- Every milestone should have a clear test that proves it works
- Prefer simple, working code over complex, theoretical optimizations
- Don't optimize prematurely: get it working, then measure, then optimize

## Go Code Conventions

### Project Structure

```
poly/
├── cmd/poly/main.go           # Entry point, config, orchestration
├── internal/
│   ├── polymarket/            # API clients (Gamma, CLOB)
│   ├── scanner/               # Pre-filter pipeline
│   ├── analysis/              # Claude, trust scoring, market intelligence
│   ├── pipeline/              # Waterfall orchestration
│   ├── sizing/                # Kelly formula, position sizing
│   ├── paper/                 # Paper trading simulation
│   ├── execution/             # Real CLOB order placement
│   ├── monitor/               # Rebalancing, UMA disputes, resolution tracking
│   ├── risk/                  # Circuit breakers, capital lockup limits
│   ├── telegram/              # Bot framework
│   ├── store/                 # SQLite persistence
│   └── backtest/              # Backtesting engine
├── templates/
│   ├── pillarlab_prompt.md    # PillarLab prompt template (Layer 1: Forecaster)
│   └── claude_auditor_prompt.md # Claude Auditor prompt template (Layer 3: Auditor + Reasoner)
├── config.yaml                # Thresholds, tiers, parameters
├── .env                       # API keys (gitignored)
└── *.md                       # Documentation
```

### Style Guidelines

- **Naming**: Be explicit. `calculateFractionalKellyWithMakerRebate()` is better than `calcKelly()`
- **Error handling**: Always return errors, never panic except in `main.go` init
- **Logging**: Use structured logging (zerolog or slog) with context fields:
  ```go
  log.Info().
      Str("market_id", marketID).
      Float64("price", price).
      Str("tier", "A").
      Msg("Market passed pre-filter")
  ```
- **Comments**: Explain *why*, not *what*. The code shows what; comments explain intent.
  - Example: `// Queue jump: +$0.001 increases fill rate 3× for negligible cost`
- **Constants**: Define magic numbers as named constants with comments
  ```go
  const (
      GoldenZoneMin = 0.20  // $0.20: lower bound of profitable price range
      GoldenZoneMax = 0.40  // $0.40: upper bound before fee impact dominates
      MakerQueueJump = 0.001 // $0.001: minimal bid increase for queue priority
  )
  ```

### Concurrency

- Use goroutines for independent API calls (scanning, order book fetches)
- **NEVER share state without synchronization**:
  - Use `sync.Mutex` for complex state
  - Use `atomic` operations for counters (nonce)
  - Use channels for goroutine communication
- Rate limiting: wrap all API clients with `golang.org/x/time/rate` token bucket
- Thread-safe nonce: use `atomic.AddUint64()`, persist to DB on shutdown

### Testing

- Unit tests for pure functions (Kelly formula, theta calculation, fee model)
- Integration tests for API clients (use mock servers or test mode where available)
- Simulation tests for execution (paper fills with realistic latency/slippage)
- Every validation gate should have a corresponding test

## API Integration Guidelines

### Polymarket API

- **Gamma API** (`/markets`): paginated, 100 markets/call, rate limit 100/min
  - Parse `outcomePrices` carefully: it's a JSON string inside JSON
  - Check BOTH `closed` and `active` fields AND verify order book is not empty
  - Markets can close early if event resolves before `endDate`
- **CLOB API** (`/book`, `/order`): rate limit 60 orders/min, 100 public/min
  - Always implement exponential backoff on 429
  - Order signing uses EIP-712 via `go-order-utils` (official library)
  - Heartbeat every 10s to keep orders alive
- **Pre-emptive rate limiting**: use token bucket, don't wait for 429s

### Three-Signal Architecture

The waterfall pipeline uses three independent AI engines, each with a distinct role:

| Engine | Role | Strength | Weakness |
|--------|------|----------|----------|
| Perplexity | Researcher | Finds current facts, cites sources | Can't reason deeply about what facts mean |
| PillarLab | Forecaster | Pattern matching, probabilistic factor weights | Misses current events, possible training bias |
| Claude + Tavily | Auditor + Reasoner | Deep logical reasoning, cross-referencing | Only as good as Tavily's search results |

**Claude's unique value** (things the other two can't do):
1. **Resolution trap detection** — edge cases where outcome happens but market resolves differently
2. **Cross-signal reasoning** — combining Perplexity facts with PillarLab factors to find overlooked interactions
3. **Bayesian updating** — properly updating PillarLab's prior with Perplexity's fresh evidence
4. **Contradiction detection** — finding where sources disagree and determining who is right

**Current approach**: All three engines are manual (paste into web UI, copy structured JSON output). Automate via API only after proving value and comparing responses across sources.

### Perplexity (Manual → API Later)

- Manual: paste market questions into Perplexity web, copy research output
- Role: fact-finding and current events research
- Feed output into Claude Auditor as `PERPLEXITY_OUTPUT`

### PillarLab (Manual → API Later)

- No API — operated manually via pre-configured prompt template (`templates/pillarlab_prompt.md`)
- Structured JSON output with factor weights + probability
- **Critical validation**: After 50-100 predictions, check `/accuracy` to verify claimed 64% win rate
- If PillarLab doesn't deliver → fallback to Claude or Polyseer for Layer 1 signals
- Feed output into Claude Auditor as `PILLARLAB_OUTPUT`

### Claude Auditor (Manual → API Later)

- **Manual first**: use Claude subscription (Opus) to avoid API costs during validation
- Prompt template: `templates/claude_auditor_prompt.md`
- Receives both Perplexity and PillarLab outputs, produces final audited probability
- **Automate when**: manual comparison shows Claude Auditor consistently adds value (catches traps, resolves contradictions, improves calibration)
- When automated: use Extended Thinking Mode, budget ~$0.01-0.05 per market
- Timeout: 30s (Extended Thinking is slower)

### Tavily API

- **Ultra-fast mode** only, `topic: "news"`, budget $0.008/search
- Used by Claude Auditor to resolve contradictions between Perplexity and PillarLab
- Manual phase: Claude subscription includes web search — use that instead of Tavily API
- Typical usage: 2-5 searches/day, cost <$0.05/day

## Risk Management Rules (Non-Negotiable)

### Circuit Breakers

- **3 consecutive losses** → halt trading for 24h, alert via Telegram
- **5% daily drawdown** → halt trading for 24h, alert via Telegram
- **BTC/ETH >2% move in 5 minutes** → cancel all resting orders, alert
- Once halted, require explicit `/resume` command (no auto-resume)

### Position Limits

- **Max 5 concurrent positions** (diversification)
- **Max 15% of bankroll per position** (no single-market overexposure)
- **Min $5 per position** (below this, fees destroy edge)
- **Tier B capital lockup: ≤40% of bankroll** (ensure 60% stays liquid for Tier A)
- **Tier B resolution: ≤30 days** (prevent long-term capital freeze)

### DCA (Dollar-Cost Averaging) Rules

- Only trigger DCA if price drops >5% AND market internals confirm "no news"
- DCA size: 50% of initial position
- Max 3 DCA entries per position
- Total position never >2× initial allocation
- **HARD STOP: Never DCA if price drops below $0.15** (event likely not happening)

### Profit Taking (Tier A Only)

- Markets resolving in >30 days:
  - +33% from entry → sell 30%
  - +67% from entry → sell 30% more
  - Hold remaining 40% to resolution
- Markets resolving in <30 days: hold 100% to resolution
- Tier B: always hold 100% to resolution (can't exit efficiently)

### UMA Dispute Handling

- Check for active disputes every 30 minutes (alongside rebalancing)
- If dispute filed on held position:
  - Immediate Telegram alert: "🚨 UMA DISPUTE on [market]"
  - Tier A: recommend immediate exit (sell at current bid)
  - Tier B: alert only (may not be able to exit)
- Track dispute outcomes to improve source trust scoring over time

## Telegram Bot Commands

| Command | Purpose | Phase |
|---------|---------|-------|
| `/scan` | Run market scan, show Golden Zone candidates | 1+ |
| `/import <JSON>` | Parse PillarLab output, run full waterfall pipeline | 2+ |
| `/approve [N]` | Approve top N signals for execution | 3+ |
| `/status` | Show open positions, P&L, balance, Tier B lockup % | 1+ |
| `/lockup` | Detailed Tier B capital allocation breakdown | 3+ |
| `/accuracy` | PillarLab prediction accuracy report | 4+ |
| `/trade SIDE market_id` | Manual trade override (use sparingly) | 5+ |
| `/cancel <order_id>` | Cancel specific order | 5+ |
| `/halt` | Emergency stop: cancel all orders, pause trading | 5+ |
| `/resume` | Resume trading after halt | 5+ |
| `/pnl` | Daily/weekly P&L summary with fee/rebate breakdown | 3+ |
| `/help` | List all commands | 1+ |

## Data Persistence

### SQLite Schema (Development)

**markets**: market_id, question, category, end_date, liquidity, active, closed, created_at
**scans**: scan_id, market_id, price, tier, theta, depth_at_bid, spread, last_trade, scan_date
**predictions**: prediction_id, market_id, pillarlab_probability, pillarlab_edge, market_price, our_side, confidence, imported_at, resolved_at, actual_outcome, pnl
**trades**: trade_id, market_id, side, entry_price, size, shares, fee, clarity_score, theta, tier, status, created_at, filled_at, resolved_at, outcome, pnl
**nonce**: id=1, value (single row, updated atomically)

### Redis (Production, Phase 6)

- Order book cache: `orderbook:<market_id>` (TTL 30s)
- Nonce: distributed counter via `INCR` command
- Rate limit state: token bucket per endpoint

## Configuration Management

### .env (Never Commit)

```
POLYMARKET_API_KEY=...
POLYMARKET_PRIVATE_KEY=...
CLAUDE_API_KEY=...
TAVILY_API_KEY=...
TELEGRAM_BOT_TOKEN=...
```

### config.yaml (Commit)

```yaml
golden_zone:
  min: 0.20
  max: 0.40

liquidity_tiers:
  tier_a_min: 50000  # >$50K
  tier_b_min: 5000   # $5K-$50K
  tier_b_lockup_pct: 0.40  # Max 40% in Tier B
  tier_b_max_days: 30

sizing:
  kelly_fraction: 0.25
  min_size: 5
  max_size_pct: 0.15
  max_positions: 5

theta_decay:  # Time to resolution modifiers
  7d: 1.00
  14d: 0.90
  30d: 0.75
  60d: 0.50
  180d: 0.25
  default: 0.10

risk:
  circuit_breaker_losses: 3
  circuit_breaker_drawdown: 0.05
  volatility_threshold: 0.02  # BTC/ETH 2% in 5min
  dca_threshold: 0.05  # Price drop >5%
  dca_stop_price: 0.15  # Never DCA below $0.15
  dca_size_fraction: 0.50
  dca_max_entries: 3
  slippage_tolerance: 0.03  # Max 3% price move during order placement

spread:
  min_maker_profit: 0.03  # 3 cents minimum bid-ask

execution:
  graceful_shutdown:
    cancel_tier_a_orders: true  # Cancel Tier A on shutdown
    keep_tier_b_orders: true    # Keep Tier B alive (hold-to-resolution)

monitoring:
  rebalance_interval: 30m
  uma_check_interval: 30m
```

## Security Best Practices

- **Never store secrets in code** — always use `.env` for development or AWS Secrets Manager for production
- **Never log API keys or private keys** (use `[REDACTED]` in logs)
- **Never commit `.env` to git** (add to `.gitignore` immediately)
- EIP-712 signing for CLOB orders: use official `go-order-utils`, never roll your own crypto
- Validate all external input (PillarLab JSON, Telegram commands, API responses)
- Rate limiting prevents accidental DoS of Polymarket APIs

## Workflow Guidelines

### Daily Workflow (25-35 minutes)

1. **Morning (automated)**: Bot scans at 08:00 UTC → Telegram notification
2. **Review (5 min)**: Check candidates, note high theta/liquidity markets
3. **Perplexity (5-10 min)**: Research top 10-15 candidates — current events, key facts, source citations
4. **PillarLab (10-15 min)**: Paste same markets into PillarLab with template — get factor weights + probability
5. **Claude Auditor (5-10 min)**: Paste both outputs into Claude Opus with auditor template — get audited probability, resolution traps, contradiction analysis
6. **Compare (2 min)**: Review all three signals side by side. Where they agree = high confidence. Where they disagree = dig deeper or skip.
7. **Import (2 min)**: `/import <JSON>` → bot runs stale price check + scoring
8. **Approve (1 min)**: Review signals, `/approve N` to execute
9. **Monitor (passive)**: Telegram updates on fills, rebalancing, circuit breakers

### Phase Progression

- **Phase 1-2**: Build scanner + waterfall pipeline, test with scans only (no execution)
- **Phase 3**: Paper trading with simulated fills for 3-5 days minimum
- **Phase 4**: Harden with circuit breakers, backtest with 2+ weeks data
- **Phase 5**: Go live with $200-300 for 1 week, then scale to $1,000 if validated
- **Phase 6**: Optimize based on live data, scale bankroll as strategy proves profitable

### Before Going Live Checklist

- [ ] 2+ weeks of paper trading without critical bugs
- [ ] Backtesting shows win rate >45% with positive expected value
- [ ] All circuit breakers tested and working
- [ ] Maker-only execution verified (never accidentally taker)
- [ ] Slippage tolerance prevents price stalking (tested with price jump scenarios)
- [ ] Nonce management proven thread-safe
- [ ] Graceful shutdown tested: nonce persists, Tier A orders canceled, Tier B kept alive
- [ ] Rate limiting handles burst load without 429 errors
- [ ] UMA dispute monitoring in place
- [ ] Telegram alerts working for all critical events
- [ ] PillarLab accuracy tracked (know if it's worth the cost)

## Do's and Don'ts

### DO:

- Test every new feature in paper mode first
- Log every decision point (why was a market filtered? why was DCA triggered?)
- Monitor PillarLab accuracy — track every prediction vs. outcome
- Use structured logging with context fields for debugging
- Implement graceful degradation when APIs fail
- Ask for confirmation before risky actions (large orders, manual overrides)
- Keep daily workflow under 30 minutes (automation is the goal)

### DON'T:

- Never use market orders (always maker limits)
- Never DCA below $0.15 (falling knife protection)
- Never exceed position limits (max 5 positions, 15% per position, 40% Tier B lockup)
- Never skip validation gates (each phase builds on previous)
- Never store secrets in code — use `.env` or AWS Secrets Manager
- Never commit `.env`, API keys, or private keys to git
- Never panic in code (return errors, handle gracefully)
- Never chase runaway prices (implement slippage tolerance)
- Don't optimize before measuring (get it working first)
- Don't add complexity until simple version is validated

## Performance Targets

- **Full scan (500 markets)**: <90 seconds
- **Waterfall pipeline (10 markets)**: <2 minutes (includes Claude + market intel)
- **Order placement**: <1 second (maker limit order submission)
- **Daily workflow**: <30 minutes total human time
- **Cost per day**: <$2 (Claude + Tavily + PillarLab amortized)

## Known Limitations & Workarounds

- **Gamma API doesn't support price filtering**: filter client-side after fetching all markets
- **CLOB `/book` may return stale data**: implement stale price check on `/import`, consider WebSocket in Phase 6
- **PillarLab has no API**: manual workflow, enforce structured JSON output in prompt template
- **UMA dispute detection**: if no API exists, implement as manual `/dispute` command rather than automated monitoring
- **go-order-utils signing**: only official library that works with current Polymarket contracts — if it fails, must debug (no alternative)

## Success Metrics

### Phase 3-4 (Paper Trading):
- Win rate >45% (below this, rethink signal generation)
- Pipeline runs reliably without crashes for 5+ days
- Daily workflow <30 minutes

### Phase 5 (Live Trading):
- Maker orders 100% (never taker)
- Fill rate matches paper simulation
- No circuit breaker false positives
- P&L ≥ break-even after 1 week

### Phase 6 (Scaled):
- Win rate maintained or improved with more data
- Bankroll growth steady (target: $1K → $2K → $5K+)
- PillarLab accuracy >55% (vs. claimed 64%)
- Total daily time investment ≤30 minutes

---

**Last Updated**: 2026-03-24
**Current Phase**: Phase 0 (Pre-Development)
**Next Milestone**: Milestone 1.1 — "Can we talk to Polymarket?"
