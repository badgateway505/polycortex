package monitor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

const (
	// Rate budget: 80% of 100 req/min = 80 usable req/min
	rateBudgetPerMin = 80.0
	// Two CLOB calls per market per poll: trades + order book (YES side).
	reqsPerPoll = 2
	// Minimum interval between polls even with 1 market, to avoid hammering.
	minPollInterval = 1500 * time.Millisecond
	// Default alert cooldown: same rule won't re-fire for a market within this period.
	defaultCooldown = 10 * time.Minute
	// How many alerts to keep in the feed.
	alertFeedCap = 200
)

// WatchedMarket is the per-market monitor configuration stored in the watchlist.
type WatchedMarket struct {
	MarketID    string
	Signal      scanner.Signal
	EnabledRules []string                      // rule IDs active for this market (nil = all rules enabled)
	RuleParams  map[string]map[string]float64  // ruleID → param overrides
	AddedAt     time.Time
}

// Monitor polls watched markets on a dynamic interval and fires alerts when rules trigger.
type Monitor struct {
	mu        sync.RWMutex
	watchlist map[string]*WatchedMarket  // marketID → config
	states    map[string]*MarketState    // marketID → live state

	alerts    []Alert // ring buffer, capped at alertFeedCap
	alertMu   sync.RWMutex

	clob       *polymarket.CLOBClient
	logger     *slog.Logger
	cooldown   time.Duration

	stopCh chan struct{}
	doneCh chan struct{}
}

// New creates a Monitor. Call Start() to begin polling.
func New(clob *polymarket.CLOBClient, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		watchlist: make(map[string]*WatchedMarket),
		states:    make(map[string]*MarketState),
		clob:      clob,
		logger:    logger,
		cooldown:  defaultCooldown,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start launches the background polling goroutine.
func (m *Monitor) Start() {
	go m.run()
}

// Stop signals the polling loop to exit and waits for it to finish.
func (m *Monitor) Stop() {
	close(m.stopCh)
	<-m.doneCh
}

// Watch adds a market to the watchlist. If the market is already watched, it is a no-op.
func (m *Monitor) Watch(sig scanner.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := sig.Market.ID
	if _, exists := m.watchlist[id]; exists {
		return
	}
	m.watchlist[id] = &WatchedMarket{
		MarketID:   id,
		Signal:     sig,
		RuleParams: make(map[string]map[string]float64),
		AddedAt:    time.Now(),
	}
	m.states[id] = newMarketState(sig)
	m.logger.Info("market added to watchlist", "market_id", id, "question", sig.Market.Question)
}

// Unwatch removes a market from the watchlist and deletes its state.
func (m *Monitor) Unwatch(marketID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.watchlist, marketID)
	delete(m.states, marketID)
	m.logger.Info("market removed from watchlist", "market_id", marketID)
}

// Watchlist returns a copy of the current watchlist.
func (m *Monitor) Watchlist() []*WatchedMarket {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*WatchedMarket, 0, len(m.watchlist))
	for _, wm := range m.watchlist {
		out = append(out, wm)
	}
	return out
}

// SetRules overwrites the enabled rules and param overrides for a watched market.
func (m *Monitor) SetRules(marketID string, enabledRules []string, params map[string]map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wm, ok := m.watchlist[marketID]
	if !ok {
		return fmt.Errorf("market %s not in watchlist", marketID)
	}
	wm.EnabledRules = enabledRules
	if params != nil {
		wm.RuleParams = params
	}
	return nil
}

// Alerts returns the most recent alerts across all markets (newest first).
func (m *Monitor) Alerts() []Alert {
	m.alertMu.RLock()
	defer m.alertMu.RUnlock()
	out := make([]Alert, len(m.alerts))
	copy(out, m.alerts)
	return out
}

// AlertsForMarket returns alerts for a specific market (newest first).
func (m *Monitor) AlertsForMarket(marketID string) []Alert {
	m.alertMu.RLock()
	defer m.alertMu.RUnlock()
	var out []Alert
	for _, a := range m.alerts {
		if a.MarketID == marketID {
			out = append(out, a)
		}
	}
	return out
}

// State returns a copy of the current MarketState for a market, or nil if not watched.
func (m *Monitor) State(marketID string) *MarketState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[marketID]
}

// ─── polling loop ─────────────────────────────────────────────────────────────

func (m *Monitor) run() {
	defer close(m.doneCh)
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		m.mu.RLock()
		markets := make([]*WatchedMarket, 0, len(m.watchlist))
		for _, wm := range m.watchlist {
			markets = append(markets, wm)
		}
		m.mu.RUnlock()

		if len(markets) == 0 {
			select {
			case <-m.stopCh:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// Dynamic interval: spread N markets' 2-call cost over the rate budget.
		// interval = (N × reqsPerPoll) / (rateBudgetPerMin / 60)
		intervalSec := float64(len(markets)) * float64(reqsPerPoll) / (rateBudgetPerMin / 60.0)
		interval := time.Duration(intervalSec * float64(time.Second))
		if interval < minPollInterval {
			interval = minPollInterval
		}
		perMarket := interval / time.Duration(len(markets))

		for _, wm := range markets {
			select {
			case <-m.stopCh:
				return
			default:
			}
			m.pollMarket(wm)
			time.Sleep(perMarket)
		}
	}
}

func (m *Monitor) pollMarket(wm *WatchedMarket) {
	m.mu.RLock()
	state, ok := m.states[wm.MarketID]
	m.mu.RUnlock()
	if !ok {
		return
	}

	// ── 1. Fetch trades (public Data API — no auth required) ────────────────
	conditionID := wm.Signal.Market.ConditionID
	trades, err := m.clob.GetMarketTradesEvents(conditionID, 100)
	if err != nil {
		m.logger.Warn("failed to fetch trades", "market_id", wm.MarketID, "err", err)
	} else {
		m.mu.Lock()
		state.appendTrades(trades)
		m.mu.Unlock()
	}

	// ── 2. Fetch YES order book ───────────────────────────────────────────────
	if wm.Signal.YesTokenID != "" {
		rawBook, err := m.clob.GetOrderBook(wm.Signal.YesTokenID)
		if err != nil {
			m.logger.Warn("failed to fetch YES book", "market_id", wm.MarketID, "err", err)
		} else {
			snap, err := polymarket.ParseBookSnapshot(rawBook)
			if err != nil {
				m.logger.Warn("failed to parse YES book", "market_id", wm.MarketID, "err", err)
			} else {
				m.mu.Lock()
				state.PrevYesBook = state.YesBook
				state.YesBook = snap
				if snap.MidPrice > 0 {
					state.appendPricePoint(PricePoint{
						MidPrice:  snap.MidPrice,
						YesBid:    snap.BestBid,
						YesAsk:    snap.BestAsk,
						SampledAt: time.Now(),
					})
				}
				m.mu.Unlock()
			}
		}
	}

	m.mu.Lock()
	state.LastPollAt = time.Now()
	m.mu.Unlock()

	// ── 3. Evaluate rules ─────────────────────────────────────────────────────
	m.evaluateRules(wm, state)
}

func (m *Monitor) evaluateRules(wm *WatchedMarket, state *MarketState) {
	enabledSet := make(map[string]bool)
	for _, id := range wm.EnabledRules {
		enabledSet[id] = true
	}
	allEnabled := len(wm.EnabledRules) == 0 // nil/empty = all rules on

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, rule := range AllRules() {
		if !allEnabled && !enabledSet[rule.ID()] {
			continue
		}

		// Skip if still within cooldown for this rule on this market.
		if !cooldownElapsed(state.AlertHistory, rule.ID(), m.cooldown) {
			continue
		}

		// Merge per-market param overrides on top of rule defaults.
		params := mergeParams(rule, wm.RuleParams[rule.ID()])

		alert := rule.Evaluate(state, params)
		if alert == nil {
			continue
		}

		// Fill in a unique ID and record cooldown.
		alert.ID = fmt.Sprintf("%s_%s_%d", wm.MarketID, rule.ID(), time.Now().UnixMilli())
		alert.TriggeredAt = time.Now()
		recordFired(state.AlertHistory, rule.ID())

		m.logger.Info("alert fired",
			"rule", rule.ID(),
			"market_id", wm.MarketID,
			"severity", alert.Severity,
			"message", alert.Message,
		)

		m.pushAlert(*alert)
	}
}

func (m *Monitor) pushAlert(a Alert) {
	m.alertMu.Lock()
	defer m.alertMu.Unlock()
	// Prepend newest first, cap at alertFeedCap.
	m.alerts = append([]Alert{a}, m.alerts...)
	if len(m.alerts) > alertFeedCap {
		m.alerts = m.alerts[:alertFeedCap]
	}
}
