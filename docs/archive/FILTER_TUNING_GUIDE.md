# Filter Tuning Guide

Quick reference for adjusting Stage 1 Discovery Filters without code changes.

## Editing Filters

All filters are in `config.yaml`. After editing, just rebuild and run:

```bash
go build -o poly ./cmd/poly
./poly scan
```

## Filter Parameters

### 1. Golden Zone Price Range

```yaml
golden_zone:
  min: 0.20  # Lower bound
  max: 0.40  # Upper bound
```

**Purpose**: Target the optimal price range for maker fees
**Current**: $0.20-$0.40
**Adjust if**:
- Too few markets → widen range (e.g., 0.15-0.45)
- Too many low-quality markets → narrow range (e.g., 0.25-0.35)

---

### 2. Volume Filter (24h)

```yaml
scanner:
  volume_min_24h: 500  # Minimum 24h trading volume
```

**Purpose**: Avoid "ghost" markets with no real trading
**Current**: $500
**Adjust if**:
- Markets lack liquidity → increase (e.g., 1000)
- Missing good markets → decrease (e.g., 250)

---

### 3. Horizon Filter (Days to Resolution)

```yaml
scanner:
  horizon_min_days: 3   # Minimum days
  horizon_max_days: 30  # Maximum days
```

**Purpose**:
- `min`: Avoid noise from markets resolving too soon
- `max`: Avoid stagnation from markets too far out

**Current**: 3-30 days
**Adjust if**:
- Too few markets found → increase max (e.g., 60 days)
- Too many long-term markets → decrease max (e.g., 14 days)
- Getting too many near-term markets → increase min (e.g., 7 days)

**Common presets**:
- Short-term trading: 3-14 days
- Medium-term: 7-30 days
- Flexible: 3-60 days

---

### 4. Liquidity Tiers

```yaml
liquidity_tiers:
  tier_a_min: 50000  # >$50K = Tier A (high liquidity)
  tier_b_min: 5000   # $5K-$50K = Tier B (moderate)
  # Below tier_b_min = SKIP (too low)
```

**Purpose**: Classify markets by exit liquidity
**Current**: Tier A >$50K, Tier B $5K-$50K
**Adjust if**:
- Need more Tier A markets → lower tier_a_min (e.g., 30000)
- Tier B too risky → raise tier_b_min (e.g., 10000)

---

### 5. Category Filter

```yaml
scanner:
  preferred_categories:
    - "Politics"
    - "Crypto"
    - "Business"
    - "Science"
    - "Sports"
    - "Economics"

  excluded_categories:
    - "Pop Culture"
    - "Meme"
    - "Entertainment"
```

**Purpose**: Focus on high-signal categories
**Current**: Prefer fundamental categories, exclude memes/pop culture
**Adjust if**:
- Want specific categories → add to preferred (e.g., "Technology")
- Getting bad markets → add to excluded (e.g., "Celebrity")

**Note**: Category filter only excludes Tier B markets. Tier A markets with high liquidity pass regardless.

---

### 6. Authoritative Sources (Not Yet Active)

```yaml
scanner:
  authoritative_sources:
    - "Associated Press"
    - "Reuters"
    - "Bloomberg"
    - "ESPN"
    - "NBA.com"
    # etc.
```

**Purpose**: Only trade markets with authoritative resolution sources
**Current**: Defined but not enforced (API limitation)
**Adjust**: Add/remove sources as needed for future implementation

---

### 7. Rejection Logging

```yaml
scanner:
  log_rejects: true  # Enable detailed rejection logging
```

**Purpose**: Debug why markets are filtered out
**Current**: Enabled
**Adjust if**:
- Don't need rejection details → set to `false`
- Want to analyze filter effectiveness → keep `true`

**Output**: `scan-results/scan-TIMESTAMP-rejects.json`

---

## Common Tuning Scenarios

### Scenario 1: "Not finding enough markets"

1. **Widen horizon**: `horizon_max_days: 60` (was 30)
2. **Lower volume**: `volume_min_24h: 250` (was 500)
3. **Widen Golden Zone**: `max: 0.45` (was 0.40)

### Scenario 2: "Getting too many low-quality markets"

1. **Raise volume**: `volume_min_24h: 1000` (was 500)
2. **Raise Tier B min**: `tier_b_min: 10000` (was 5000)
3. **Narrow horizon**: `horizon_max_days: 14` (was 30)

### Scenario 3: "Want only short-term trades"

1. **Shorten horizon**: `horizon_max_days: 14` (was 30)
2. **Require higher volume**: `volume_min_24h: 1000` (was 500)
3. **Prefer Tier A**: Manually filter Tier B from results

### Scenario 4: "Want to test specific categories"

1. **Clear excluded_categories**: `excluded_categories: []`
2. **Set preferred_categories** to only what you want
3. **Note**: Many markets have empty category field

---

## Testing Filter Changes

After adjusting `config.yaml`:

```bash
# Rebuild
go build -o poly ./cmd/poly

# Test with small sample
./poly scan --limit 100

# Check rejection reasons
cat scan-results/scan-*-rejects.json | jq '.[].reason' | sort | uniq -c

# Run full scan
./poly scan
```

---

## Monitoring Filter Effectiveness

Check `scan-TIMESTAMP.md` after each scan for:

1. **Rejection Summary**: Which filters are rejecting most markets?
2. **Market Quality**: Are passed markets actually tradeable?
3. **Tier Distribution**: Are you getting enough Tier A vs Tier B?
4. **Days to Resolve**: Are markets in your target timeframe?

---

## Default Configuration (Current)

```yaml
golden_zone:
  min: 0.20
  max: 0.40

liquidity_tiers:
  tier_a_min: 50000
  tier_b_min: 5000

scanner:
  volume_min_24h: 500
  horizon_min_days: 3
  horizon_max_days: 30
  log_rejects: true
```

This configuration is **conservative** - designed to avoid low-quality markets at the cost of finding fewer opportunities. Adjust based on your risk tolerance and capital size.

---

**Quick Test**: After changing config, run `./poly scan --limit 100` to test without fetching all 500 markets.
