package monitor

import (
	"time"

	"github.com/badgateway/poly/internal/polymarket"
	"github.com/badgateway/poly/internal/scanner"
)

const (
	defaultTradeBufferSize  = 500 // trades kept in memory per market
	defaultPriceHistorySize = 200 // mid-price snapshots kept per market
)

// Trade is a single executed trade stored in the ring buffer.
type Trade struct {
	ID        string
	Side      string    // "BUY" or "SELL" (taker perspective)
	Outcome   string    // "YES" or "NO"
	Price     float64
	Size      float64   // shares
	ValueUSD  float64   // Price × Size
	Timestamp time.Time
}

// PricePoint is one mid-price sample taken at a single poll.
type PricePoint struct {
	MidPrice  float64
	YesBid    float64
	YesAsk    float64
	SampledAt time.Time
}

// MarketState holds all live data collected for a single watched market.
// Rules read from this struct; the polling loop writes to it.
type MarketState struct {
	MarketID     string
	Signal       scanner.Signal
	Trades       []Trade     // ring buffer — newest appended, capped at TradeBufferSize
	YesBook      *polymarket.BookSnapshot
	PrevYesBook  *polymarket.BookSnapshot // book from previous poll (for change-detection rules)
	NoBook       *polymarket.BookSnapshot
	PrevNoBook   *polymarket.BookSnapshot
	PriceHistory []PricePoint // ring buffer — newest appended, capped at PriceHistorySize
	LastPollAt   time.Time
	AlertHistory map[string]time.Time // ruleID → last fired (cooldown)

	tradeBufferSize  int
	priceHistorySize int
}

// newMarketState initialises state for a newly watched market.
func newMarketState(sig scanner.Signal) *MarketState {
	return &MarketState{
		MarketID:         sig.Market.ID,
		Signal:           sig,
		Trades:           make([]Trade, 0, defaultTradeBufferSize),
		PriceHistory:     make([]PricePoint, 0, defaultPriceHistorySize),
		AlertHistory:     make(map[string]time.Time),
		tradeBufferSize:  defaultTradeBufferSize,
		priceHistorySize: defaultPriceHistorySize,
	}
}

// appendTrades merges newly fetched trades into the ring buffer.
// It deduplicates by Trade.ID so re-fetching the same window is safe.
func (s *MarketState) appendTrades(incoming []polymarket.TradeEvent) {
	// Build a set of already-known IDs for fast dedup.
	known := make(map[string]struct{}, len(s.Trades))
	for _, t := range s.Trades {
		known[t.ID] = struct{}{}
	}

	for _, ev := range incoming {
		if _, dup := known[ev.ID]; dup {
			continue
		}
		s.Trades = append(s.Trades, Trade{
			ID:        ev.ID,
			Side:      ev.Side,
			Outcome:   ev.Outcome,
			Price:     ev.Price,
			Size:      ev.Size,
			ValueUSD:  ev.ValueUSD,
			Timestamp: ev.Timestamp,
		})
		known[ev.ID] = struct{}{}
	}

	// Cap buffer size — drop oldest entries from the front.
	if len(s.Trades) > s.tradeBufferSize {
		s.Trades = s.Trades[len(s.Trades)-s.tradeBufferSize:]
	}
}

// appendPricePoint adds a mid-price sample and caps the history buffer.
func (s *MarketState) appendPricePoint(p PricePoint) {
	s.PriceHistory = append(s.PriceHistory, p)
	if len(s.PriceHistory) > s.priceHistorySize {
		s.PriceHistory = s.PriceHistory[len(s.PriceHistory)-s.priceHistorySize:]
	}
}

// TradesInWindow returns trades within the last d duration, newest-first slice order.
func (s *MarketState) TradesInWindow(d time.Duration) []Trade {
	cutoff := time.Now().Add(-d)
	var result []Trade
	for i := len(s.Trades) - 1; i >= 0; i-- {
		if s.Trades[i].Timestamp.Before(cutoff) {
			break
		}
		result = append(result, s.Trades[i])
	}
	return result
}

// PricePointsInWindow returns price history samples within the last d duration.
func (s *MarketState) PricePointsInWindow(d time.Duration) []PricePoint {
	cutoff := time.Now().Add(-d)
	var result []PricePoint
	for i := len(s.PriceHistory) - 1; i >= 0; i-- {
		if s.PriceHistory[i].SampledAt.Before(cutoff) {
			break
		}
		result = append(result, s.PriceHistory[i])
	}
	return result
}

// CurrentMidPrice returns the most recent YES mid-price, or 0 if no history yet.
func (s *MarketState) CurrentMidPrice() float64 {
	if len(s.PriceHistory) == 0 {
		return 0
	}
	return s.PriceHistory[len(s.PriceHistory)-1].MidPrice
}
