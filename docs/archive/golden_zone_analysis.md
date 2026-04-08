# Golden Zone Market Analysis
**Date:** 2026-03-24
**Source:** 500 active Polymarket markets
**Filter:** YES price 0.20-0.40 (Golden Zone)
**Results:** 22 markets (4.4% of total)

---

## Summary Statistics

- **Total markets scanned:** 500
- **Golden Zone candidates:** 22 (4.4%)
- **With >$10K liquidity:** 17 (77%)
- **With >$50K liquidity:** 13 (59%)
- **With >$100K liquidity:** 10 (45%)

**Categories:**
- Sports: 11 markets (50%)
- Politics: 9 markets (41%)
- Entertainment/Other: 2 markets (9%)

---

## Market Quality Assessment

### 🟢 TIER 1: High Liquidity, Clear Resolution (Good for Trading)

1. **Bayern Munich wins Champions League** [22%]
   - Liquidity: $523K | Vol 24h: Unknown
   - Resolution: 68 days (May 31, 2026)
   - ✅ Clear resolution criteria (UEFA official results)
   - ✅ High liquidity, tight spreads
   - ⚠️ Sports betting - need edge on odds

2. **Arsenal wins Champions League** [26%]
   - Liquidity: $314K
   - Resolution: 68 days
   - ✅ Same tournament, liquid market

3. **J.D. Vance wins 2028 GOP nomination** [36%]
   - Liquidity: $307K
   - Resolution: 959 days (2028 convention)
   - ✅ Clear resolution (official GOP nomination)
   - ⚠️ Very long time horizon, many unknowns

4. **Gavin Newsom wins 2028 Dem nomination** [24%]
   - Liquidity: $716K (HIGHEST)
   - Resolution: 959 days
   - ✅ Very liquid, clear resolution
   - ⚠️ Long time horizon

5. **Russia-Ukraine ceasefire by end 2026** [34%]
   - Liquidity: $377K
   - Resolution: 282 days
   - ⚠️ Resolution criteria might be ambiguous ("ceasefire" definition)
   - Need to check exact rules

6. **Oklahoma City Thunder win NBA Finals** [37%]
   - Liquidity: $291K
   - Resolution: 99 days (June 2026)
   - ✅ Clear resolution, high liquidity
   - Sports odds - compare to Vegas lines

7. **Colorado Avalanche win Stanley Cup** [20%]
   - Liquidity: $232K
   - Resolution: 98 days
   - ✅ Clear resolution, high liquidity

8. **Marco Rubio wins 2028 GOP nomination** [27%]
   - Liquidity: $256K
   - Resolution: 959 days
   - ✅ Clear resolution but far future

---

### 🟡 TIER 2: Medium Liquidity, Mostly Clear Resolution

9. **Real Madrid wins La Liga** [21%]
   - Liquidity: $158K
   - Resolution: 67 days
   - ✅ Clear resolution criteria

10. **Balance of Power: R Senate, D House** [35%]
    - Liquidity: $98K
    - Resolution: 224 days (Nov 2026 midterms)
    - ✅ Clear resolution (election results)

11. **Viktor Orbán next PM of Hungary** [35%]
    - Liquidity: $99K
    - Resolution: 19 days (April 2026 election)
    - ✅ Near-term, clear resolution
    - 🚀 SHORT TIME HORIZON - good for testing

12. **Cavaliers win East Finals** [22%]
    - Liquidity: $79K
    - Resolution: 81 days

13. **Celtics win East Finals** [34%]
    - Liquidity: $61K
    - Resolution: 81 days

14. **Zelenskyy out by end 2026** [22%]
    - Liquidity: $51K
    - Resolution: 282 days
    - ⚠️ Ambiguous resolution (resignation? election? coup?)
    - Need to check exact criteria

15. **Spurs win West Finals** [21%]
    - Liquidity: $43K
    - Resolution: 84 days

---

### 🔴 TIER 3: Lower Liquidity, Resolution Concerns

16. **Callum Turner as next James Bond** [23%]
    - Liquidity: $13K
    - Resolution: 98 days
    - ⚠️ LOW LIQUIDITY
    - ⚠️ Resolution criteria unclear ("announced" - by whom?)

17. **Sweden qualifies for 2026 World Cup** [32%]
    - Liquidity: $6.5K
    - Resolution: 19 days
    - ⚠️ Very low liquidity

18. **Poland qualifies for 2026 World Cup** [37%]
    - Liquidity: $5.5K
    - Resolution: 19 days
    - ⚠️ Very low liquidity

19. **Cooper Flagg wins NBA ROY** [30%]
    - Liquidity: $4.9K
    - Resolution: 55 days
    - ⚠️ Very low liquidity

20. **Harvey Weinstein: no prison time** [34%]
    - Liquidity: $2.3K
    - Resolution: -83 days ⚠️ **ALREADY PASSED?**
    - ❌ Likely stale market

21. **Harvey Weinstein: 20-30 years** [22%]
    - Liquidity: $1.2K
    - Resolution: -83 days ⚠️ **ALREADY PASSED?**
    - ❌ Likely stale market

22. **Ukraine qualifies for 2026 World Cup** [26%]
    - Liquidity: $2K
    - Resolution: 19 days
    - ⚠️ Very low liquidity

---

## Key Insights for Trading Strategy

### ✅ What Works in Golden Zone:

1. **Sports markets dominate** (50% of opportunities)
   - Clear resolution criteria
   - High liquidity
   - Short-medium time horizons (2-3 months)
   - Can cross-reference with Vegas odds for edge detection

2. **Long-term political markets** (2028 nominations)
   - Very high liquidity ($250-700K)
   - Clear resolution
   - BUT: 2.5 years away = lots can change

3. **Near-term political events** (Hungary election in 19 days)
   - Good testing ground
   - Fast capital recycling

### ❌ Red Flags Found:

1. **Stale markets** (Harvey Weinstein - already past end date)
   - Need to filter by `endDate > now` more carefully

2. **Low liquidity traps** (<$10K)
   - 5 markets have <$10K liquidity
   - Wide spreads, hard to exit

3. **Ambiguous resolution** (Ukraine ceasefire, James Bond casting)
   - Need Claude Layer 2 to catch these

4. **Category is null** for all markets
   - Can't filter by category from API
   - Need to infer from question text

---

## Recommended Test Cases for Pipeline

### Test Set 1: HIGH CONFIDENCE (use for initial validation)
```json
[
  {
    "market": "Colorado Avalanche win Stanley Cup",
    "price": 0.20,
    "liquidity": "$232K",
    "days": 98,
    "why": "High liquidity, clear resolution, short horizon, can verify odds with Vegas"
  },
  {
    "market": "Viktor Orbán next PM of Hungary",
    "price": 0.35,
    "liquidity": "$99K",
    "days": 19,
    "why": "Near-term, clear resolution, fast validation"
  }
]
```

### Test Set 2: MEDIUM RISK (use after validation)
```json
[
  {
    "market": "Russia-Ukraine ceasefire by end 2026",
    "price": 0.34,
    "liquidity": "$377K",
    "days": 282,
    "why": "Test Claude's ability to detect ambiguous resolution criteria"
  },
  {
    "market": "Gavin Newsom wins 2028 Dem nomination",
    "price": 0.24,
    "liquidity": "$716K",
    "days": 959,
    "why": "Highest liquidity, test long-horizon strategy"
  }
]
```

### Test Set 3: AVOID (use to test filters)
```json
[
  {
    "market": "Harvey Weinstein no prison time",
    "price": 0.34,
    "liquidity": "$2.3K",
    "days": -83,
    "why": "Should be filtered out: stale + low liquidity"
  },
  {
    "market": "Callum Turner as James Bond",
    "price": 0.23,
    "liquidity": "$13K",
    "days": 98,
    "why": "Should be flagged: low liquidity + ambiguous resolution"
  }
]
```

---

## Recommendations for Strategy Refinement

### 1. Liquidity threshold should be >$50K, not >$10K
- Only 45% of Golden Zone markets have >$100K liquidity
- Sweet spot appears to be $50-300K for maker orders

### 2. Focus on sports markets initially
- 50% of opportunities
- Clear resolution, short horizons
- Can validate edge against Vegas/betting markets

### 3. Add end date validation
- Filter out markets with `endDate < now`
- 2 markets already past resolution date

### 4. Consider 0.15-0.45 range
- Current 0.20-0.40 only captures 22 markets from 500
- Expanding slightly could give more opportunities

### 5. Time horizon sweet spot: 20-100 days
- Short enough for fast capital recycling
- Long enough for edge to materialize
- Avoid 2+ year markets (too many unknowns)

---

## Next Steps

1. ✅ Dataset saved to `golden_zone_research.json`
2. ⏭️ Test PillarLab on 5-10 of these markets
3. ⏭️ Build scanner bot to automate this filtering
4. ⏭️ Run Claude analysis on "Test Set 1" markets
5. ⏭️ Start paper trading with 2-3 positions
