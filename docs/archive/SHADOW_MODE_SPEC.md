# Shadow Mode Specification — Signal Tracker Architecture

**Date:** 2026-03-25
**Status:** Roadmap Updated
**Context:** Replaced naive paper trading (simulated fills/slippage) with sophisticated signal tracking that answers: "If we had acted on this signal, would we have profited?"

---

## 🎯 Core Philosophy

**Traditional Paper Trading Problems:**
- ❌ Assumes instant fills (ignores queue position, depth)
- ❌ Assumes exact price (ignores slippage)
- ❌ Static snapshot (doesn't capture market dynamics)
- ❌ Binary outcome (just "win" or "loss")

**Shadow Mode Solution:**
- ✅ Track all signals (approved + rejected)
- ✅ Measure theoretical P&L after resolution
- ✅ Analyze filter effectiveness (are we too strict?)
- ✅ Calibrate thresholds based on real data

---

## 📊 Database Architecture (Two Tables)

### Table 1: `signal_tracker` (Approved Signals)
**Purpose:** Track signals we would have traded

**Key Fields:**
- `signal_id` — Primary key
- `market_id`, `market_question`, `market_category`
- `signal_timestamp` — When signal was generated
- `desired_output` — 'YES' or 'NO' (the side we want)
- `actual_chance` — Market price at signal time (e.g., 0.195)
- `threshold_chance` — Nearest Golden Zone boundary (0.20 or 0.40)
- `predicted_probability` — AI's estimate
- `predicted_edge_pct` — AI probability - market price
- `clarity` — 0.0-1.0 verifiability score
- `is_paper_trade` — TRUE = shadow/paper, FALSE = real trade
- `market_resolved`, `final_outcome`, `would_have_profited`, `theoretical_pnl_pct`

**Deduplication:** `UNIQUE(market_id, signal_timestamp)` with UPSERT logic

---

### Table 2: `signal_rejects` (Filtered Signals - Shadow Mode)
**Purpose:** Track what we filtered out (for calibration)

**Key Fields:**
- `reject_id` — Primary key
- `market_id`, `market_question`, `market_category`
- `signal_timestamp`
- `desired_output`, `actual_chance`, `threshold_chance`
- **`rejection_layer`** — ENUM: `'pre-ai'` or `'post-ai'`
  - **Pre-AI:** Filtered by Go logic (Price, Liquidity, Horizon) to save costs
  - **Post-AI:** Analyzed by Claude but failed on Edge/Clarity
- **`rejection_reasons`** — TEXT[] array (multiple tags)
  - Example: `['LOW_CLARITY', 'LOW_EDGE']`
- `filter_value_actual` — JSONB: `{"clarity": 0.62, "edge": 0.02}`
- `filter_value_threshold` — JSONB: `{"clarity": 0.70, "edge": 0.03}`
- **`predicted_probability`, `clarity`** — NULL for pre-ai rejects
- **`shadow_mode_enabled`** — Toggle for tracking outcomes
- `market_resolved`, `final_outcome`, `would_have_profited`, `theoretical_pnl_pct`

**Deduplication:** `UNIQUE(market_id, signal_timestamp)`

---

## 🔍 Filter Reasons (ENUM)

### Pre-AI Filters (Go Logic — Cost Saver):
| Reason | Meaning | Threshold |
|--------|---------|-----------|
| `OUTSIDE_GOLDEN_ZONE` | Price not in $0.20-$0.40 | Golden Zone boundaries |
| `LOW_LIQUIDITY` | Total liquidity too low | <Tier threshold |
| `THIN_TRUE_DEPTH` | Depth within ±2% of mid too low | <$5K |
| `THIN_DEPTH_VOLUME` | D/V ratio too low | <0.01 |
| `SHORT_HORIZON` | Too close to resolution | <3 days |
| `LONG_HORIZON` | Too far from resolution | >30 days |
| `LOW_VOLUME_24H` | Daily volume too low | <$500 |
| `WEAK_RESOLUTION_SOURCE` | No authoritative source | Not AP/Reuters/Gov/etc. |
| `EXCLUDED_CATEGORY` | Pop culture/meme (unless Tier A) | Category blacklist |

### Post-AI Filters (After Claude Analysis):
| Reason | Meaning | Threshold |
|--------|---------|-----------|
| `LOW_CLARITY` | Clarity score too low | <0.70 |
| `LOW_EDGE` | Predicted edge too small | <3% |
| `STALE_PRICE` | Price moved during analysis | >5% change |
| `TIER_B_LOCKUP` | Would exceed Tier B allocation | >40% of bankroll |
| `MAX_POSITIONS` | Already at position limit | ≥5 positions |
| `LOW_THETA` | Time decay too low | <0.25 |

---

## ⚙️ Shadow Mode Toggle

### Configuration (`config.yaml`):
```yaml
paper_trading:
  shadow_mode_enabled: true              # Track filtered signals
  resolution_check_interval: 24h         # How often to check resolutions

discovery_filter:  # Pre-AI cost saver
  golden_zone_min: 0.20
  golden_zone_max: 0.40
  volume_24h_min: 500
  horizon_min_days: 3
  horizon_max_days: 30
  excluded_categories:
    - "Pop Culture"
    - "Meme"
  trusted_sources:
    - "AP"
    - "Reuters"
    - "Bloomberg"
    - "Official Government Portal"
    - "ESPN"
```

### Telegram Commands:
```
/shadow on          # Enable shadow mode tracking
/shadow off         # Disable shadow mode tracking
/shadow             # Show current status
```

---

## 🔄 Workflow

### Daily Workflow (20-30 min):

**1. Morning Scan (Automated at 08:00 UTC):**
```bash
/scan
# Scans 500 markets
# Pre-AI filter: 478 rejected → logged to signal_rejects (rejection_layer='pre-ai')
# Candidates: 22 markets → send to next stage
```

**2. PillarLab Analysis (Manual, 10-15 min):**
```
- Paste 22 candidates into PillarLab with template
- Get structured JSON output
```

**3. Import & Post-AI Filter:**
```bash
/import <pillarlab_json>
# Claude analyzes 22 markets
# Post-AI filter: 12 rejected → logged to signal_rejects (rejection_layer='post-ai')
# Approved: 10 signals → logged to signal_tracker
```

**4. Approve (Paper Mode):**
```bash
/approve 10
# "📝 PAPER: Tracked 10 signals in signal_tracker"
```

**5. Daily Resolution Check (Automated Background Job):**
```sql
-- Check if any tracked markets resolved
UPDATE signal_tracker SET
    market_resolved = TRUE,
    final_outcome = 'YES',
    would_have_profited = TRUE,
    theoretical_pnl_pct = 0.042
WHERE market_id IN (SELECT id FROM resolved_markets);

-- Check rejected signals (ONLY if shadow_mode_enabled = TRUE)
UPDATE signal_rejects SET
    market_resolved = TRUE,
    final_outcome = 'YES',
    would_have_profited = FALSE,
    theoretical_pnl_pct = -0.29
WHERE shadow_mode_enabled = TRUE
AND market_id IN (SELECT id FROM resolved_markets);
```

---

## 📊 Analysis Queries

### Query 1: Approved Signal Performance
```sql
SELECT
    COUNT(*) as total,
    SUM(CASE WHEN would_have_profited THEN 1 ELSE 0 END) as wins,
    AVG(theoretical_pnl_pct) * 100 as avg_return_pct
FROM signal_tracker
WHERE market_resolved = TRUE;
```

**Expected Output:**
```
total: 23
wins: 14
avg_return_pct: +4.2%
```

**Interpretation:** Win rate 61% (14/23), positive expected value ✅

---

### Query 2: Filter Effectiveness (Requires Shadow Mode)
```sql
SELECT
    UNNEST(rejection_reasons) as reason,
    COUNT(*) as times_rejected,
    SUM(CASE WHEN would_have_profited THEN 1 ELSE 0 END) as missed_wins,
    AVG(theoretical_pnl_pct) * 100 as avg_return_pct
FROM signal_rejects
WHERE shadow_mode_enabled = TRUE
AND market_resolved = TRUE
GROUP BY reason
ORDER BY avg_return_pct DESC;
```

**Expected Output:**
```
reason              | times_rejected | missed_wins | avg_return_pct
--------------------|----------------|-------------|----------------
LOW_CLARITY         | 12             | 8           | +5.1% ⚠️
THIN_LIQUIDITY      | 8              | 2           | -1.3% ✅
STALE_PRICE         | 5              | 2           | +0.8%
LOW_THETA           | 3              | 1           | -2.1% ✅
```

**Interpretation:**
- ⚠️ `LOW_CLARITY` filter is too strict (rejecting +5.1% avg return)
- ✅ `THIN_LIQUIDITY` filter is working (rejected signals lost -1.3%)

---

### Query 3: Pre-AI vs Post-AI Effectiveness
```sql
SELECT
    rejection_layer,
    COUNT(*) as total,
    AVG(theoretical_pnl_pct) * 100 as avg_return_pct
FROM signal_rejects
WHERE shadow_mode_enabled = TRUE
AND market_resolved = TRUE
GROUP BY rejection_layer;
```

**Expected Output:**
```
rejection_layer | total | avg_return_pct
----------------|-------|----------------
pre-ai          | 78    | -0.8%  ✅ (correctly filtered)
post-ai         | 22    | +2.3%  ⚠️ (maybe too strict)
```

**Interpretation:** Pre-AI filters are working (rejecting unprofitable), but Post-AI filters might be too conservative.

---

### Query 4: Near-Miss Analysis (Just Outside Golden Zone)
```sql
SELECT
    COUNT(*) as total,
    AVG(actual_chance) as avg_price,
    AVG(threshold_chance) as avg_threshold,
    AVG(theoretical_pnl_pct) * 100 as avg_return_pct
FROM signal_rejects
WHERE 'OUTSIDE_GOLDEN_ZONE' = ANY(rejection_reasons)
AND shadow_mode_enabled = TRUE
AND market_resolved = TRUE
AND ABS(actual_chance - threshold_chance) < 0.02;  -- Within 2 cents
```

**Expected Output:**
```
total: 8
avg_price: 0.188
avg_threshold: 0.20
avg_return_pct: +4.8%
```

**Interpretation:** 8 markets rejected within 2 cents of $0.20 boundary would have returned +4.8%. Consider widening Golden Zone to $0.18-$0.42.

---

## 🎯 Telegram Commands & Output

### `/signals` — Show Tracked Signals
```
📊 APPROVED SIGNALS (Last 14 days)
Total: 23 | Resolved: 18 | Pending: 5

Market                                  | Side | Entry | Status
----------------------------------------|------|-------|--------
Colorado Avalanche Stanley Cup          | YES  | $0.204| ✅ WON (+14.2%)
Hungary PM Viktor Orbán                 | YES  | $0.355| ❌ LOST (-35.5%)
Arsenal Champions League                | YES  | $0.275| ⏳ Pending
```

---

### `/analysis` — Performance Summary
```
📊 APPROVED SIGNALS (Last 14 days)
Total: 23 | Resolved: 18 | Pending: 5

✅ Wins: 11 (61%)
❌ Losses: 7 (39%)
📈 Avg Return: +4.2%

By Category:
- Sports: 8 signals, 65% win rate, +5.1% avg
- Politics: 10 signals, 60% win rate, +3.8% avg
- Crypto: 5 signals, 55% win rate, +3.2% avg

Status: ✅ Strategy is profitable
Next: Run /calibrate to optimize filters
```

---

### `/rejections` — Filter Effectiveness
```
🔍 REJECTION ANALYSIS (Last 14 days)
Shadow Mode: ✅ ENABLED

Filter Reason       | Rejected | Wins | Avg Return
--------------------|----------|------|------------
LOW_CLARITY         | 12       | 8    | +5.1% ⚠️
THIN_LIQUIDITY      | 8        | 2    | -1.3% ✅
STALE_PRICE         | 5        | 2    | +0.8%
LOW_THETA           | 3        | 1    | -2.1% ✅

⚠️ LOW_CLARITY filter may be too strict
💡 Suggestion: Lower clarity threshold from 0.70 to 0.65
```

---

### `/calibrate` — Adjustment Suggestions
```
🎯 FILTER CALIBRATION SUGGESTIONS

Based on 14 days of shadow mode data:

1. ⚠️ LOW_CLARITY filter is too strict
   - Rejecting signals with +5.1% avg return
   - Current threshold: 0.70
   - Suggested: 0.65
   - Impact: +3-5 more trades/week

2. ✅ THIN_LIQUIDITY filter is working correctly
   - Rejected signals lost -1.3% on average
   - Keep current threshold: $5K

3. 🔍 Near-miss analysis: OUTSIDE_GOLDEN_ZONE
   - 8 markets rejected within 2 cents of $0.20 boundary
   - Avg return if taken: +4.8%
   - Consider: Widen Golden Zone to $0.18-$0.42

Apply changes? /calibrate apply
```

---

## 🚀 Rate Limit Safety

**Question:** Will checking hundreds of filtered signals burn Polymarket rate limits?

### Rate Limit Math:
- **Gamma API:** 100 req/min
- **Daily resolution check:** ~30-45 markets (approved + rejected with shadow mode)
- **API calls:** 1 per market (GET /markets?id=xxx)
- **Time:** 45 calls at 100/min = <1 minute
- **Budget usage:** <1% of daily rate limit

**Verdict:** ✅ Shadow mode is safe — negligible rate limit impact

---

## 📈 2-Week Shadow Mode Timeline

| Day | Activity | Data Collected |
|-----|----------|----------------|
| **1-7** | Run daily workflow | 10 approved/day, 25 rejected/day |
| **7** | Run `/calibrate` | "LOW_CLARITY too strict (+5.1%)" |
| **8-14** | Continue workflow | More data collection |
| **14** | Run `/calibrate` again | "LOW_CLARITY still too strict (+4.9%)" ← Consistent |
| **15** | Apply calibration | Lower clarity 0.70 → 0.65 |
| **16-21** | Test new threshold | More trades approved |
| **21** | Final `/calibrate` | "LOW_CLARITY now neutral (+0.8%)" ← Fixed |

---

## ✅ Success Metrics (Phase 3 Complete Gate)

**Before moving to Phase 4:**
- [ ] 14 days of shadow mode data collected
- [ ] Both tables populated with 50+ total signals
- [ ] Approved signals: 10+ resolved markets
- [ ] Shadow mode: 100+ rejected signals tracked
- [ ] **Win rate:** Approved signals >50%
- [ ] **Avg return:** Approved signals >0% (positive EV)
- [ ] **Consistency:** `/calibrate` suggestions similar on Day 7 vs Day 14
- [ ] **Calibration:** At least 1 filter adjusted based on data
- [ ] **Time:** Daily workflow <30 minutes
- [ ] **Stability:** No critical bugs

---

## 🎯 Key Advantages vs. Traditional Paper Trading

| Metric | Traditional Paper Trading | Shadow Mode |
|--------|---------------------------|-------------|
| **Build Time** | 2-3 days | 3-4 hours |
| **Accuracy** | Assumes instant fills, exact prices | Measures real outcomes |
| **Insights** | Binary win/loss | Filter effectiveness, near-miss analysis |
| **Calibration** | Manual guessing | Data-driven threshold adjustments |
| **Cost** | High (complex simulation) | Low (simple tracking) |
| **Focus** | Execution mechanics | Strategy validation |

---

## 🔧 Implementation Notes

### 1. Multi-Stage Filtering
- **Pre-AI rejects:** Log immediately with `predicted_probability=NULL`
- **Post-AI rejects:** Log after Claude with full AI metadata

### 2. Multiple Rejection Reasons
- Use `TEXT[]` array: `['LOW_CLARITY', 'LOW_EDGE']`
- Store all applicable filters, not just first failure

### 3. UPSERT Logic
- Use `ON CONFLICT (market_id, signal_timestamp) DO UPDATE`
- Prevents duplicates if same market scanned multiple times

### 4. Shadow Mode Toggle
- Only track outcomes if `shadow_mode_enabled = TRUE`
- Saves API calls when not analyzing filters

### 5. Transition to Micro-Live
- `is_paper_trade` field allows same schema for real trades
- No schema changes needed when moving to Phase 5

---

**Last Updated:** 2026-03-25
**Status:** Roadmap updated, ready for implementation
**Next Step:** Implement Milestone 3.2 (signal tracker tables + resolution job)
