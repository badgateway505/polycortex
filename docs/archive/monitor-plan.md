
  ---
  Market Monitor & Rule Engine — Build Plan

  Goal

  Real-time monitoring of saved markets with a configurable rule engine. Each saved market gets its own enabled
  rules. Polling rate is calculated dynamically from portfolio size to stay within CLOB rate limits.

  ---
  Rate Limiting Strategy

  CLOB public limit: 100 req/min. Safety factor: 80% → 80 req/min usable.

  Per-market cost per poll: 2 requests (trades + order book).

  poll_interval_seconds = (portfolio_size × 2) / (80/60)
                        = portfolio_size × 1.5 seconds

  1 market  → poll every 1.5s   (real-time)
  2 markets → poll every 3s
  5 markets → poll every 7.5s
  10 markets → poll every 15s
  20 markets → poll every 30s

  Recalculate interval every time a market is added/removed from portfolio.

  ---
  Architecture

  internal/monitor/
    ├── monitor.go        — Monitor struct, Start/Stop, dynamic polling loop
    ├── state.go          — MarketState, trade ring buffer, book snapshots
    ├── alert.go          — Alert struct, severity, cooldown logic
    ├── rules.go          — Rule interface + registry
    ├── rules_whale.go    — Large trade detection rules
    ├── rules_volume.go   — Volume accumulation + spike rules
    ├── rules_price.go    — Price movement rules
    ├── rules_book.go     — Order book depth/imbalance rules
    └── rules_pattern.go  — Multi-trade pattern rules

  ---
  Rule Interface

  type Rule interface {
      ID()          string         // Unique snake_case identifier e.g. "whale_single_buy"
      Name()        string         // Human label e.g. "Whale Single Buy"
      Category()    RuleCategory   // whale | volume | price | book | pattern
      Params()      []RuleParam    // Configurable parameters with defaults
      Evaluate(state *MarketState, params map[string]float64) *Alert
  }

  type RuleParam struct {
      Key          string
      Label        string
      DefaultValue float64
      Unit         string  // "USD", "minutes", "percent", "count"
      Min, Max     float64
  }

  Every rule is stateless — state lives in MarketState, rules just evaluate it.

  ---
  Market State (per watched market)

  type MarketState struct {
      MarketID     string
      Signal       scanner.Signal
      Trades       []Trade         // Ring buffer, last 500 trades (~1 hour)
      YesBook      *BookSnapshot
      NoBook       *BookSnapshot
      PriceHistory []PricePoint    // Mid-price samples, last 200 polls
      LastPollAt   time.Time
      AlertHistory map[string]time.Time  // ruleID → last fired (for cooldown)
  }

  type Trade struct {
      ID        string
      Side      string    // "BUY" or "SELL"
      Outcome   string    // "YES" or "NO"
      Price     float64
      Size      float64   // shares
      ValueUSD  float64   // price × size
      Timestamp time.Time
  }

  ---
  Full Rule Catalog

  Whale Rules

  ┌────────────────────┬───────────────────────────────────────────┬────────────────────────────────────────┐
  │      Rule ID       │                Description                │                 Params                 │
  ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────┤
  │ whale_single_trade │ Single trade > $X                         │ min_usd=500                            │
  ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────┤
  │ whale_accumulation │ N trades > $X each in last T minutes      │ min_usd=200, min_count=3,              │
  │                    │                                           │ window_min=10                          │
  ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────┤
  │ whale_exit         │ Large sell > $X (position unwinding)      │ min_usd=500                            │
  ├────────────────────┼───────────────────────────────────────────┼────────────────────────────────────────┤
  │ whale_relative     │ Single trade > X% of hourly average       │ pct_of_hourly=200                      │
  │                    │ volume                                    │                                        │
  └────────────────────┴───────────────────────────────────────────┴────────────────────────────────────────┘

  Volume Rules

  ┌─────────────────────┬───────────────────────────────────────────────────┬────────────────────────────┐
  │       Rule ID       │                    Description                    │           Params           │
  ├─────────────────────┼───────────────────────────────────────────────────┼────────────────────────────┤
  │ volume_spike        │ Volume in T min > N× previous T min               │ window_min=5, multiplier=3 │
  ├─────────────────────┼───────────────────────────────────────────────────┼────────────────────────────┤
  │ volume_directional  │ YES (or NO) volume > X% of total in T min         │ window_min=10, pct=75      │
  ├─────────────────────┼───────────────────────────────────────────────────┼────────────────────────────┤
  │ volume_total        │ Total volume > $X in last T minutes               │ window_min=5, min_usd=1000 │
  ├─────────────────────┼───────────────────────────────────────────────────┼────────────────────────────┤
  │ volume_silence      │ Volume < $X for last T minutes (going quiet)      │ window_min=15, max_usd=50  │
  ├─────────────────────┼───────────────────────────────────────────────────┼────────────────────────────┤
  │ volume_acceleration │ Each successive T-min window larger than previous │ window_min=5, windows=3    │
  └─────────────────────┴───────────────────────────────────────────────────┴────────────────────────────┘

  Price Rules

  ┌─────────────────┬─────────────────────────────────────────────┬─────────────────────────────────────────┐
  │     Rule ID     │                 Description                 │                 Params                  │
  ├─────────────────┼─────────────────────────────────────────────┼─────────────────────────────────────────┤
  │ price_move_up   │ Price rose > X% in T minutes                │ pct=3, window_min=10                    │
  ├─────────────────┼─────────────────────────────────────────────┼─────────────────────────────────────────┤
  │ price_move_down │ Price dropped > X% in T minutes             │ pct=3, window_min=10                    │
  ├─────────────────┼─────────────────────────────────────────────┼─────────────────────────────────────────┤
  │ price_cross     │ Price crossed threshold (golden zone        │ threshold=0.40                          │
  │                 │ boundary)                                   │                                         │
  ├─────────────────┼─────────────────────────────────────────────┼─────────────────────────────────────────┤
  │ price_reversal  │ Price moved >X% then reversed >Y% within T  │ move_pct=3, reversal_pct=2,             │
  │                 │ min                                         │ window_min=15                           │
  ├─────────────────┼─────────────────────────────────────────────┼─────────────────────────────────────────┤
  │ price_stall     │ Price unchanged >X% for T minutes           │ max_change_pct=0.5, window_min=20       │
  │                 │ (pre-move?)                                 │                                         │
  └─────────────────┴─────────────────────────────────────────────┴─────────────────────────────────────────┘

  Order Book Rules

  ┌────────────────────┬────────────────────────────────────────────┬──────────────┐
  │      Rule ID       │                Description                 │    Params    │
  ├────────────────────┼────────────────────────────────────────────┼──────────────┤
  │ book_depth_drop    │ Best bid depth dropped > X% vs last poll   │ pct=30       │
  ├────────────────────┼────────────────────────────────────────────┼──────────────┤
  │ book_wall_appeared │ Single order > $X at best bid/ask          │ min_usd=2000 │
  ├────────────────────┼────────────────────────────────────────────┼──────────────┤
  │ book_wall_removed  │ Large order that existed is now gone       │ min_usd=2000 │
  ├────────────────────┼────────────────────────────────────────────┼──────────────┤
  │ book_imbalance     │ Bid depth / ask depth > X (without trades) │ ratio=3.0    │
  ├────────────────────┼────────────────────────────────────────────┼──────────────┤
  │ book_spread_widen  │ Spread widened > X% vs baseline            │ pct=50       │
  └────────────────────┴────────────────────────────────────────────┴──────────────┘

  Pattern Rules

  ┌─────────────────────┬─────────────────────────────────────────┬─────────────────────────────────────────┐
  │       Rule ID       │               Description               │                 Params                  │
  ├─────────────────────┼─────────────────────────────────────────┼─────────────────────────────────────────┤
  │ pattern_consecutive │ N consecutive trades same direction     │ min_count=5                             │
  ├─────────────────────┼─────────────────────────────────────────┼─────────────────────────────────────────┤
  │ pattern_iceberg     │ Many small trades ($X-$Y) within T      │ min_usd=50, max_usd=200, count=8,       │
  │                     │ seconds (bot)                           │ window_sec=30                           │
  ├─────────────────────┼─────────────────────────────────────────┼─────────────────────────────────────────┤
  │ pattern_bracket     │ Simultaneous large YES and NO buys      │ min_usd=300, window_sec=60              │
  │                     │ (hedging/arb)                           │                                         │
  ├─────────────────────┼─────────────────────────────────────────┼─────────────────────────────────────────┤
  │ pattern_stop_hunt   │ Sharp dip/spike then immediate reversal │ move_pct=2, reversal_sec=120            │
  ├─────────────────────┼─────────────────────────────────────────┼─────────────────────────────────────────┤
  │ pattern_step_ladder │ Series of trades each at slightly       │ min_count=4, window_min=5               │
  │                     │ higher price                            │                                         │
  └─────────────────────┴─────────────────────────────────────────┴─────────────────────────────────────────┘

  ---
  Watchlist — Session Storage

  // Add to session.go
  type WatchedMarket struct {
      MarketID     string
      Signal       scanner.Signal
      EnabledRules []string                       // rule IDs active for this market
      RuleParams   map[string]map[string]float64  // ruleID → param overrides
      AddedAt      time.Time
  }

  // Session additions:
  watchlist     map[string]*WatchedMarket  // marketID → config
  alertFeed     []Alert                    // Recent alerts (last 200)

  ---
  Alert Structure

  type AlertSeverity string
  const (
      SeverityInfo    AlertSeverity = "info"     // Interesting, worth watching
      SeverityWarning AlertSeverity = "warning"  // Unusual activity
      SeverityAlert   AlertSeverity = "alert"    // Act now
  )

  type Alert struct {
      ID          string
      MarketID    string
      MarketQ     string        // Question for display
      RuleID      string
      RuleName    string
      Severity    AlertSeverity
      Side        string        // "YES", "NO", or "BOTH"
      Message     string        // Human-readable e.g. "$1,240 whale buy on YES"
      Data        map[string]any // Raw data for Sonnet context
      TriggeredAt time.Time
  }

  Cooldown per rule per market: configurable in config.yaml. Default 10 min — same rule won't fire twice in 10
  min for same market.

  ---
  New API Endpoints

  POST   /api/watch/{id}              — Add market to portfolio
  DELETE /api/watch/{id}              — Remove market from portfolio
  GET    /api/watch                   — List all watched markets
  PATCH  /api/watch/{id}/rules        — Enable/disable rules + set params
  GET    /api/alerts                  — Recent alert feed (all markets)
  GET    /api/alerts/{id}             — Alerts for specific market
  POST   /api/alerts/{id}/analyze     — Send alert context to Sonnet for analysis
  GET    /api/rules                   — List all available rules with params

  ---
  New CLOB Method Needed

  // Add to internal/polymarket/clob.go
  func (c *CLOBClient) GetMarketTradesEvents(conditionID string) ([]TradeEvent, error)

  type TradeEvent struct {
      ID          string
      Side        string    // "BUY" or "SELL"
      Outcome     string    // "YES" or "NO"
      Price       float64
      Size        float64
      ValueUSD    float64
      Timestamp   time.Time
  }

  ---
  Monitor Polling Loop

  // Single goroutine, dynamic interval
  func (m *Monitor) run() {
      for {
          markets := m.session.GetWatchlist()
          if len(markets) == 0 {
              time.Sleep(5 * time.Second)
              continue
          }

          interval := calculateInterval(len(markets))  // 2 req × N / 80rpm

          for _, wm := range markets {
              m.pollMarket(wm)
              time.Sleep(interval / time.Duration(len(markets)))
          }
      }
  }

  func (m *Monitor) pollMarket(wm *WatchedMarket) {
      // 1. Fetch trades + order book (2 CLOB calls)
      // 2. Update MarketState ring buffer
      // 3. Evaluate all enabled rules
      // 4. Fire alerts with cooldown check
      // 5. Push alerts to session feed
  }

  ---
  Frontend Changes

  1. Signal card → add "Watch" button (star icon). Shows portfolio count.
  2. Portfolio tab → new tab in left panel alongside Alpha/Shadow showing watched markets
  3. Alert feed → persistent panel at bottom or sidebar showing latest alerts with timestamp, market, rule name,
   message
  4. Market detail → when viewing a watched market:
    - Live trade feed (last 20 trades, auto-refreshing)
    - Active rules with their current values
    - Alert history for this market
    - "Analyze Activity" button → sends last N trades + triggered alerts to Sonnet
  5. Rule config modal → per-market: toggle each rule on/off, adjust params with sliders

  ---
  Config.yaml additions

  monitor:
    poll_safety_factor: 0.80   # Use 80% of rate limit
    alert_cooldown: 10m        # Default cooldown between same-rule alerts
    trade_buffer_size: 500     # Trades kept in memory per market
    price_history_size: 200    # Price snapshots kept per market

    # Default rule params (overridable per market)
    rules:
      whale_single_trade:
        min_usd: 500
      whale_accumulation:
        min_usd: 200
        min_count: 3
        window_min: 10
      volume_spike:
        window_min: 5
        multiplier: 3.0
      price_move_up:
        pct: 3.0
        window_min: 10
      price_move_down:
        pct: 3.0
        window_min: 10
      book_depth_drop:
        pct: 30.0

  ---
  Build Order / Tasks

  - [x] 1. internal/polymarket/clob.go — add GetMarketTradesEvents (fetch recent trades for a conditionID)
  - [x] 2. internal/monitor/alert.go — Alert struct + AlertSeverity + cooldown map helper
  - [x] 3. internal/monitor/state.go — MarketState, Trade, BookSnapshot, PricePoint + ring buffer helpers
  - [x] 4. internal/monitor/rules.go — Rule interface, RuleParam, RuleCategory, registry (AllRules slice)
  - [x] 5. internal/monitor/rules_whale.go — whale_single_trade, whale_accumulation, whale_exit, whale_relative
  - [x] 6. internal/monitor/rules_volume.go — volume_spike, volume_directional, volume_total, volume_silence, volume_acceleration
  - [x] 7. internal/monitor/rules_price.go — price_move_up, price_move_down, price_cross, price_reversal, price_stall
  - [x] 8. internal/monitor/rules_book.go — book_depth_drop, book_wall_appeared, book_wall_removed, book_imbalance, book_spread_widen
  - [x] 9. internal/monitor/rules_pattern.go — pattern_consecutive, pattern_iceberg, pattern_bracket, pattern_stop_hunt, pattern_step_ladder
  - [x] 10. internal/monitor/monitor.go — Monitor struct, dynamic polling loop, pollMarket, rule evaluation + alert dispatch
  - [x] 11. internal/web/session.go — WatchedMarket lives in monitor.go; session provides GetSignalByMarketID for watch handler
  - [x] 12. internal/web/handlers_monitor.go — 7 new REST endpoints (watch/unwatch/list, rules, alerts, set-rules)
  - [x] 13. internal/web/server.go — register new routes, inject monitor into server, start on launch
  - [x] 14. internal/web/static/app.js — Portfolio tab, real-time alert feed panel, rule config modal
  - [x] 15. config/config.go — MarketMonitorConfig struct added to AppConfig
  - [x] 16. config.yaml — market_monitor defaults block added