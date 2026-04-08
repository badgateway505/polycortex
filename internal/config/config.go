package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the entire configuration file structure
type Config struct {
	GoldenZone     GoldenZoneConfig     `yaml:"golden_zone"`
	LiquidityTiers LiquidityTiersConfig `yaml:"liquidity_tiers"`
	CategoryGate   CategoryGateConfig   `yaml:"category_gate"`
	Liveness       LivenessConfig       `yaml:"liveness"`
	Quality        QualityConfig        `yaml:"quality"`
	Distribution   DistributionConfig   `yaml:"distribution"`
	Scanner        ScannerConfig        `yaml:"scanner"` // Legacy, kept for compatibility
	Sizing         SizingConfig         `yaml:"sizing"`
	ThetaDecay     map[string]float64   `yaml:"theta_decay"`
	Risk           RiskConfig           `yaml:"risk"`
	Spread         SpreadConfig         `yaml:"spread"`
	Execution      ExecutionConfig      `yaml:"execution"`
	Monitoring     MonitoringConfig     `yaml:"monitoring"`
	MarketMonitor  MarketMonitorConfig  `yaml:"market_monitor"`
	Analysis       AnalysisConfig       `yaml:"analysis"`
}

// GoldenZoneConfig defines the price range for Golden Zone filtering
type GoldenZoneConfig struct {
	Min float64 `yaml:"min"` // Lower bound ($0.20)
	Max float64 `yaml:"max"` // Upper bound ($0.40)
}

// LiquidityTiersConfig defines liquidity tier thresholds
type LiquidityTiersConfig struct {
	TierAMin       float64 `yaml:"tier_a_min"`        // >$50K
	TierBMin       float64 `yaml:"tier_b_min"`        // $5K-$50K
	TierBLockupPct float64 `yaml:"tier_b_lockup_pct"` // Max 40% in Tier B
	TierBMaxDays   int     `yaml:"tier_b_max_days"`   // Max 30 days resolution
}

// CategoryGateConfig defines Layer 1 filtering (Category Gate)
type CategoryGateConfig struct {
	AllowedCategories  []string `yaml:"allowed_categories"`
	ExcludedCategories []string `yaml:"excluded_categories"`
}

// LivenessConfig defines Layer 2 filtering (Liveness Check)
type LivenessConfig struct {
	MinLiquidity  float64 `yaml:"min_liquidity"`   // $1,000 minimum
	MinVolume24h  float64 `yaml:"min_volume_24h"`  // $500 minimum
}

// QualityConfig defines Layer 3 filtering (Quality Gate)
type QualityConfig struct {
	HorizonMinDays       int      `yaml:"horizon_min_days"`       // 3 days minimum
	HorizonMaxDays       int      `yaml:"horizon_max_days"`       // 30 days maximum
	AuthoritativeSources []string `yaml:"authoritative_sources"`  // For future use
}

// DistributionConfig defines Layer 4 filtering (Distribution Engine)
type DistributionConfig struct {
	GoldenZoneMin     float64 `yaml:"golden_zone_min"`      // VWAP >= this
	GoldenZoneMax     float64 `yaml:"golden_zone_max"`      // VWAP <= this
	MinLiquidity      float64 `yaml:"min_liquidity"`        // $5K for Alpha
	MinTrueDepthUSD   float64 `yaml:"min_true_depth_usd"`   // $250 within ±2%
	MaxSpreadPct      float64 `yaml:"max_spread_pct"`       // 3%
	DefaultStakePct   float64 `yaml:"default_stake_pct"`    // 5% hard cap
	DefaultBalance    float64 `yaml:"default_balance"`      // $1,000
}

// AnalysisConfig defines AI analysis parameters
type AnalysisConfig struct {
	MinReanalysisInterval string `yaml:"min_reanalysis_interval"` // e.g., "30m"
	PillarlabEnabled      bool   `yaml:"pillarlab_enabled"`       // false = skip PillarLab in pipeline
}

// ScannerConfig defines pre-AI filtering rules (LEGACY — kept for compatibility)
type ScannerConfig struct {
	VolumeMin24h         float64  `yaml:"volume_min_24h"`
	HorizonMinDays       int      `yaml:"horizon_min_days"`
	HorizonMaxDays       int      `yaml:"horizon_max_days"`
	ExcludedCategories   []string `yaml:"excluded_categories"`
	LogRejects           bool     `yaml:"log_rejects"`
}

// SizingConfig defines position sizing parameters
type SizingConfig struct {
	KellyFraction float64 `yaml:"kelly_fraction"` // Fractional Kelly (0.25 = 25%)
	MinSize       float64 `yaml:"min_size"`       // Minimum position size ($5)
	MaxSizePct    float64 `yaml:"max_size_pct"`   // Max % of bankroll per position (15%)
	MaxPositions  int     `yaml:"max_positions"`  // Max concurrent positions (5)
}

// RiskConfig defines risk management rules
type RiskConfig struct {
	CircuitBreakerLosses   int     `yaml:"circuit_breaker_losses"`   // Halt after N consecutive losses
	CircuitBreakerDrawdown float64 `yaml:"circuit_breaker_drawdown"` // Halt after X% daily drawdown
	VolatilityThreshold    float64 `yaml:"volatility_threshold"`     // BTC/ETH move threshold
	DCAThreshold           float64 `yaml:"dca_threshold"`            // Price drop to trigger DCA
	DCAStopPrice           float64 `yaml:"dca_stop_price"`           // Never DCA below this price
	DCASizeFraction        float64 `yaml:"dca_size_fraction"`        // DCA size as fraction of initial
	DCAMaxEntries          int     `yaml:"dca_max_entries"`          // Max DCA entries per position
	SlippageTolerance      float64 `yaml:"slippage_tolerance"`       // Max acceptable slippage
}

// SpreadConfig defines spread requirements
type SpreadConfig struct {
	MinMakerProfit float64 `yaml:"min_maker_profit"` // Minimum bid-ask spread (3 cents)
}

// ExecutionConfig defines execution behavior
type ExecutionConfig struct {
	GracefulShutdown GracefulShutdownConfig `yaml:"graceful_shutdown"`
}

// GracefulShutdownConfig defines shutdown behavior
type GracefulShutdownConfig struct {
	CancelTierAOrders bool `yaml:"cancel_tier_a_orders"` // Cancel Tier A on shutdown
	KeepTierBOrders   bool `yaml:"keep_tier_b_orders"`   // Keep Tier B alive
}

// MonitoringConfig defines monitoring intervals
type MonitoringConfig struct {
	RebalanceInterval string `yaml:"rebalance_interval"` // Rebalancing interval (e.g., "30m")
	UMACheckInterval  string `yaml:"uma_check_interval"` // UMA dispute check interval
}

// MarketMonitorConfig configures the real-time market monitor polling engine.
type MarketMonitorConfig struct {
	PollSafetyFactor float64 `yaml:"poll_safety_factor"` // Fraction of rate limit to use (0.80 = 80%)
	AlertCooldown    string  `yaml:"alert_cooldown"`     // Min time between same-rule alerts per market
	TradeBufferSize  int     `yaml:"trade_buffer_size"`  // Trades kept in memory per market
	PriceHistorySize int     `yaml:"price_history_size"` // Price snapshots kept per market
}

// RebalanceIntervalDuration parses the rebalance interval as a time.Duration
func (mc *MonitoringConfig) RebalanceIntervalDuration() (time.Duration, error) {
	return time.ParseDuration(mc.RebalanceInterval)
}

// UMACheckIntervalDuration parses the UMA check interval as a time.Duration
func (mc *MonitoringConfig) UMACheckIntervalDuration() (time.Duration, error) {
	return time.ParseDuration(mc.UMACheckInterval)
}

// Load reads the config file from the given path and returns a Config struct
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}
