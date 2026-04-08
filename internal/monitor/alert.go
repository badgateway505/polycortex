package monitor

import (
	"time"
)

// AlertSeverity classifies how urgently an alert demands attention.
type AlertSeverity string

const (
	SeverityInfo    AlertSeverity = "info"    // Interesting, worth watching
	SeverityWarning AlertSeverity = "warning" // Unusual activity
	SeverityAlert   AlertSeverity = "alert"   // Act now
)

// Alert is fired when a rule's condition is met for a watched market.
type Alert struct {
	ID          string         `json:"id"`
	MarketID    string         `json:"market_id"`
	MarketQ     string         `json:"market_q"`   // Short question text for display
	RuleID      string         `json:"rule_id"`
	RuleName    string         `json:"rule_name"`
	Severity    AlertSeverity  `json:"severity"`
	Side        string         `json:"side"`    // "YES", "NO", or "BOTH"
	Message     string         `json:"message"` // Human-readable e.g. "$1,240 whale buy on YES"
	Data        map[string]any `json:"data"`    // Raw numeric context for Sonnet analysis
	TriggeredAt time.Time      `json:"triggered_at"`
}

// cooldownKey returns the key used to look up last-fired time in AlertHistory.
func cooldownKey(ruleID string) string {
	return ruleID
}

// cooldownElapsed returns true if enough time has passed since the rule last fired
// for this market, i.e. we are allowed to fire again.
func cooldownElapsed(history map[string]time.Time, ruleID string, cooldown time.Duration) bool {
	last, ok := history[cooldownKey(ruleID)]
	if !ok {
		return true // never fired
	}
	return time.Since(last) >= cooldown
}

// recordFired updates the cooldown history to mark that a rule just fired.
func recordFired(history map[string]time.Time, ruleID string) {
	history[cooldownKey(ruleID)] = time.Now()
}
