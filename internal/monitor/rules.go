package monitor

// RuleCategory groups rules by what they observe.
type RuleCategory string

const (
	CategoryWhale   RuleCategory = "whale"
	CategoryVolume  RuleCategory = "volume"
	CategoryPrice   RuleCategory = "price"
	CategoryBook    RuleCategory = "book"
	CategoryPattern RuleCategory = "pattern"
)

// RuleParam describes a single configurable parameter for a rule.
type RuleParam struct {
	Key          string
	Label        string
	DefaultValue float64
	Unit         string  // "USD", "minutes", "seconds", "percent", "count", "ratio"
	Min, Max     float64
}

// Rule is a stateless evaluator. All mutable data lives in MarketState.
// Evaluate returns a non-nil Alert when the rule's condition is met, nil otherwise.
type Rule interface {
	ID()       string
	Name()     string
	Category() RuleCategory
	Params()   []RuleParam
	Evaluate(state *MarketState, params map[string]float64) *Alert
}

// defaultParams builds a param map pre-populated with each rule's defaults.
// The caller may override individual keys before passing to Evaluate.
func defaultParams(r Rule) map[string]float64 {
	m := make(map[string]float64, len(r.Params()))
	for _, p := range r.Params() {
		m[p.Key] = p.DefaultValue
	}
	return m
}

// mergeParams merges per-market overrides on top of rule defaults.
func mergeParams(r Rule, overrides map[string]float64) map[string]float64 {
	m := defaultParams(r)
	for k, v := range overrides {
		m[k] = v
	}
	return m
}

// allRules is the master list of every registered rule.
// Files rules_whale.go / rules_volume.go / etc. append to this via init().
var allRules []Rule

// AllRules returns the full rule catalogue.
func AllRules() []Rule {
	return allRules
}

// RuleByID looks up a rule by its snake_case identifier.
func RuleByID(id string) (Rule, bool) {
	for _, r := range allRules {
		if r.ID() == id {
			return r, true
		}
	}
	return nil, false
}
