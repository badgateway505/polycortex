# Research Pipeline Improvements

Prioritized list of improvements to transform the research pipeline from manual copy-paste into an automated, multi-source, self-improving analysis engine.

**Guiding principle:** Each improvement should either (a) automate something currently manual, (b) add a new signal source that reveals information the existing sources miss, or (c) improve the quality of information already being collected.

---

## Task List

### Done
- [x] **Item 1 — Perplexity API**: "Research" button, auto-fetch via Sonar API, citations appended
- [x] **Item 2 — Exa.ai**: Semantic search running in parallel with Tavily on "Search News"
- [x] **Item 3 — Content Scoring Pipeline**: Recency decay + authority boost + LLM batch relevance, composite score displayed
- [x] **Item 6 — Condition Parser**: Claude Haiku parses resolution criteria, trigger conditions, edge cases, ambiguity risk
- [x] **Item 7 — Grok X Search (base)**: X sentiment search with summary, bull/bear points, notable posts
  - [ ] Expert handles: per-category list (`@NateSilver538`, `@lookonchain`, etc.) injected into Grok prompt

### Not Started
- [ ] **Item 0 — Data Persistence & Research Timeline** ← next up
  - [ ] SQLite schema: `markets`, `watchlist`, `scans`, `events` tables + indexes
  - [ ] Event types: `scan_snapshot`, `watchlist_snapshot`, `trade_flow_summary`, `tavily_search`, `perplexity_search`, `condition_parse`, `audit`, `edge_calc`, `resolution`
  - [ ] `Session` fallback: check in-memory first, fall back to latest DB event
  - [ ] Alpha History Job: re-scan known alphas every 12-24h, write `source:"history"` snapshots
  - [ ] Route change detection + alert icons (`⚠` / `↑`) in sidebar
  - [ ] Watchlist persistence: survives restarts, "SOLVED" label on resolved markets
  - [ ] UI — sidebar: price delta, score delta, "Alpha Nx" consecutive count, NEW badge
  - [ ] UI — market detail: Overview tab (delta block + latest analysis), Timeline tab, Live tab
- [ ] **Item 4 — Cross-market Comparison**
  - [ ] Kalshi client: fetch markets, keyword match
  - [ ] PredictIt client: fetch full JSON dump, search in-memory
  - [ ] Phase 1: "Compare Markets" button → side-by-side price table in detail panel
  - [ ] Phase 2: delta column in signal list (discovery signal at scan time)
- [ ] **Item 5 — Iterative Research Loop**
  - [ ] Step 1: run Perplexity + Tavily + Exa + Grok in parallel
  - [ ] Step 2: gap analysis (single Haiku call — "SUFFICIENT" or list gaps)
  - [ ] Step 3: one targeted follow-up pass via Tavily + Exa (hard max, no further loops)
  - [ ] Step 4: merge all evidence, pass "research completeness" flag to auditor
  - [ ] UI: progress states ("Searching… → Analyzing gaps… → Deep diving… → Complete")

---

## 0. Data Persistence & Research Timeline

**Status:** Not started
**Effort:** Large (8-12 hours)
**Cost:** None (SQLite, local storage)
**Priority:** HIGHEST — everything else depends on data surviving restarts

### Problem

All scan results, research, analysis, and watchlist state live in-memory (`Session` struct). Page refresh or app restart loses everything. New scans clear all caches. No history, no deltas, no continuity.

### Development Notes

- **No 24/7 server during development**: the Alpha History Job and watchlist monitor only run while the app is open. History builds up whenever the app is running. No code changes needed when moving to a VPS later.
- **DB resets are expected during debugging**: all `CREATE TABLE` statements must live in a single `initDB()` function (or `schema.sql`). Recreating the DB must be one action — delete the `.db` file and restart the app. No manual steps, no migrations needed during development. Schema will change frequently; formal migrations come later.

### Solution: SQLite + Event Timeline

#### Data Model (4 tables)

```sql
-- Registry of every market we've ever seen (immutable identity)
CREATE TABLE markets (
    market_id              TEXT PRIMARY KEY,
    question               TEXT NOT NULL,
    slug                   TEXT,
    category               TEXT,
    end_date               TIMESTAMP,
    first_seen_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved               BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_at            TIMESTAMP,
    -- Stored counter: consecutive scans in current route (alpha or shadow).
    -- Updated on every scan_snapshot write. Resets to 1 on route change.
    -- Used for sidebar display ("Alpha 4×") — stored because queried on every render.
    consecutive_route_scans INTEGER NOT NULL DEFAULT 1,
    current_route           TEXT    -- 'alpha' | 'shadow', tracks which route the counter applies to
);

-- Persistent watchlist (survives restarts, manual add/remove only).
-- Only alpha signals are added. Shadow signals not tracked here.
-- Re-add after removal uses UPSERT: INSERT ... ON CONFLICT(market_id) DO UPDATE SET removed_at=NULL, added_at=CURRENT_TIMESTAMP
CREATE TABLE watchlist (
    market_id    TEXT PRIMARY KEY REFERENCES markets(market_id),
    added_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    removed_at   TIMESTAMP  -- NULL = actively watched
);

-- Metadata per scan run (full scans and Alpha History Job runs)
CREATE TABLE scans (
    scan_id       INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    scan_type     TEXT NOT NULL DEFAULT 'full',  -- 'full' | 'history'
    total_scanned INTEGER,
    alpha_count   INTEGER,
    shadow_count  INTEGER
);

-- The timeline: every event, typed and timestamped, append-only forever
CREATE TABLE events (
    event_id     INTEGER PRIMARY KEY AUTOINCREMENT,
    market_id    TEXT NOT NULL REFERENCES markets(market_id),
    scan_id      INTEGER REFERENCES scans(scan_id),  -- NULL for manual/watchlist events
    event_type   TEXT NOT NULL,
    payload      TEXT NOT NULL,  -- JSON, schema depends on event_type
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_events_market_time ON events(market_id, created_at);
CREATE INDEX idx_events_market_type ON events(market_id, event_type, created_at);
CREATE INDEX idx_events_scan ON events(scan_id);
```

**`consecutive_route_scans`**: incremented on each `scan_snapshot` write when route matches `current_route`. Reset to 1 when route changes (also updates `current_route`). Stored because it's queried on every sidebar render for the "Alpha 4×" label.

**`been_alpha_days` / `been_shadow_days`** (not stored — derived): query at analysis time via `COUNT(DISTINCT DATE(created_at))` on `scan_snapshot` events filtered by route. Counts unique calendar days regardless of how many scans ran that day (scanned twice on Apr 3 = 1 day, not 2). Only used for backtesting and accuracy analysis, not UI display, so query cost is fine.

#### Event Types

Two categories: **scan-sourced** (written by full scan or Alpha History Job, from Gamma API data) and **watchlist-sourced** (written by CLOB polling for watched markets). They never overlap — different data sources, different purposes.

**Scan-sourced events** (have `scan_id`, written by full scan or Alpha History Job):

| event_type | When created | Payload contains |
|---|---|---|
| `scan_snapshot` | Every scan/history run, for every alpha or shadow market | Score, price, tier, theta, route, target_side, shadow_reasons, all L4 metrics, VWAP, depth, spread, activity, source: "scan"\|"history" |

**Watchlist-sourced events** (`scan_id` is NULL, written by CLOB polling loop):

| event_type | When created | Payload contains |
|---|---|---|
| `watchlist_snapshot` | Threshold crossed, filter would fail, or 30 min elapsed | Mid price, bid, ask, spread%, true depth USD, VWAP, trigger: "threshold"\|"filter_fail"\|"heartbeat", which filter failed if applicable |
| `trade_flow_summary` | Every 30 min for watched markets | Window start/end, total volume USD, trade count, buy/sell count, avg trade size, max single trade — foundation for future alert baselines |

**Research events** (`scan_id` NULL, triggered by user action or automated API call):

| event_type | When created | Payload contains |
|---|---|---|
| `tavily_search` | Tavily search runs (user-triggered or automated) | Query, answer, sources with URLs, relevance scores |
| `perplexity_search` | Perplexity API call completes (item 1) | Query, answer, citations with URLs |
| `grok_search` | Grok search runs | Query, results |
| `condition_parse` | Condition parser runs (Claude Haiku call) | Trigger conditions, resolution source, edge cases, ambiguity risk |
| `llm_paste` | User manually pastes LLM output | Source ("pillarlab"\|"perplexity"\|"grok"\|"other"), raw pasted text — used for manual copy-paste workflow only |
| `prediction` | User imports prediction JSON | Probability, confidence, key findings, uncertainty sources |
| `deep_research` | Deep research completes | Per-uncertainty results with sources |
| `audit` | Claude Auditor analysis completes | Final audited probability, reasoning, traps found |
| `edge_calc` | Edge calculation runs | Our side, our price, true prob, edge %, EV after fees |
| `resolution` | Market detected as resolved | Final outcome, resolution source, resolved_at |

New event types are added here as new items (1–7) are built — each automated API integration gets its own event type.

All events **append-only** — never overwrite. Multiple audits/searches per day stack in the same day.

#### What gets persisted vs stays in-memory

| Data | Where | Why |
|---|---|---|
| Scan results (alpha + shadow snapshots) | SQLite `events` | History, deltas, learning |
| Watchlist | SQLite `watchlist` | Survives restarts |
| Research/analysis events | SQLite `events` | Research timeline |
| Watchlist metric snapshots | SQLite `events` | Meaningful change history |
| Trade flow summaries | SQLite `events` | Alert baseline data (future) |
| Live display values (price, bid/ask, depth) | In-memory only | Ephemeral, recreated on poll |
| Current scan results | In-memory (`Session`) | Ephemeral, re-scan to populate |

#### Session changes

`Session` still exists for the **current scan's working state** (what's displayed in alpha/shadow tabs):
- `SetResult()` no longer clears all caches — saves `scan_snapshot` events to DB, updates in-memory view
- `SetTavily()`, `SetCondition()`, etc. write to DB **and** keep in-memory for current session
- On restart: alpha/shadow tabs empty (just rescan), watchlist and all history intact from DB
- `GetTavily()` etc. check in-memory first, fall back to latest DB event of that type

---

### Alpha Auto-Tracking

Every market that passes L4 (alpha route) **automatically accumulates full history** — no manual action required. Research events (tavily, conditions, audits) are written when you trigger them for any alpha market. This means:

- You don't need to "watch" a market to have research history — history exists for all alphas automatically
- Watchlist = separate upgrade tier (real-time CLOB surveillance), not a prerequisite for history
- Shadow markets get `scan_snapshot` events only (lightweight — no research events unless you add them to watchlist, which is not planned for now)

---

### Alpha History Job

**Purpose**: keep known alpha markets current between full scans, even on days you don't run a manual scan.

**How it works**:
- Runs every 12-24h (configurable), separate from the manual full scan
- Fetches fresh Gamma API data only for markets that have ever been alpha and are not resolved
- Runs the full filter pipeline (L1→L4) on each market
- Writes a `scan_snapshot` event with `source: "history"` and a new `scan_id` (type: "history")
- If a market now fails a filter → writes the snapshot with the failure reason in `shadow_reasons`, updates `current_route` and resets `consecutive_route_scans` to 1
- If route unchanged → increments `consecutive_route_scans`

**Resolution detection**: if the Gamma API returns `active: false` or `closed: true` for a known market, the Alpha History Job writes a `resolution` event, sets `resolved = TRUE` and `resolved_at` in the `markets` table, and stops tracking that market in future job runs.

**Non-overlap with watchlist**: the Alpha History Job writes `scan_snapshot` events (Gamma API data: score, tier, theta, L4 metrics). The Watchlist monitor writes `watchlist_snapshot` events (CLOB data: live price, order book). Different data sources, different event types, no collision.

---

### Watchlist Real-Time Monitoring

**Display loop** (fast, no writes): poll CLOB every N seconds. Update price, bid/ask, spread, depth, VWAP live in the market detail card. Same as current live tab behavior.

**Snapshot writing** (selective, meaningful): inside the same poll loop, check whether to write a `watchlist_snapshot` event. Write when ANY of:
1. A filter would now fail (run L2/L3 check against current metrics)
2. A value crossed a meaningful threshold (price moved >1%, depth changed >25%, spread changed >2×)
3. 30 minutes have elapsed since the last written snapshot (heartbeat)

Payload always includes which trigger fired, so you know why a snapshot was taken.

**Trade flow summaries**: write one `trade_flow_summary` event every 30 min per watched market. Aggregates trade data from the poll window (volume, count, buy/sell split, avg size). Written to DB for future alert baseline work — not shown as individual timeline rows (see UI section).

**Resolved markets**: when a market resolves, stop all polling immediately. Show "SOLVED" label in watchlist tab and market detail header. Keep all historical events in DB. Don't auto-remove from watchlist — user sees it and cleans up, or use a "clear resolved" button.

---

### Market Deduplication Across Scans

- `markets` table uses Polymarket's `market_id` as PK — natural dedup
- First time seen → INSERT into `markets` + first `scan_snapshot` event
- Subsequent scans → only new `scan_snapshot` event (market row already exists, only immutable fields stored there)
- Mutable data (price, score, tier, route) lives exclusively in events

---

### Route Change Detection & Alerts

**Detection**: route change is detected atomically at `scan_snapshot` write time — no separate comparison pass needed:

1. Before writing the new snapshot, fetch the previous one: `SELECT payload FROM events WHERE market_id=? AND event_type='scan_snapshot' ORDER BY created_at DESC LIMIT 1`
2. Compare incoming `route` (from the pipeline result) against `markets.current_route`
3. **If they differ** → route changed. Record the previous snapshot's metrics alongside the new ones to build the before/after message (e.g. `spread 2.0% → 5.3%`). Then update `markets.current_route` to the new route and reset `markets.consecutive_route_scans` to 1.
4. **If they match** → no change. Increment `markets.consecutive_route_scans`.

The before/after values in the alert message always come from step 1's payload vs the new payload — never recomputed from scratch.

**Alert icon in sidebar** (both directions):
- Alpha → Shadow: show `⚠` icon in the shadow list with the specific failing metric: `spread 2.0% → 5.3% (failed L3)`
- Shadow → Alpha: show `↑` icon in the alpha list with what recovered: `upgraded: depth $400 → $8.2K`
- Alert icon clears automatically once `consecutive_route_scans >= 2` in the new route (signal stabilized)
- Route change is **always written into the scan_snapshot payload** and visible in the timeline regardless of whether the alert icon is still showing

---

### UI Changes

#### Left Sidebar — Signal List

Each market card:

```
Will X happen by Y?                    [if watched: ●]
Score: 78 (+7)  |  YES @ $0.335
+$0.01 vs 2d ago                       18 days  Alpha 4×
```

- **Price delta**: green/red, smaller font than price, muted color (not bright)
- **Score delta**: small `(+7)` or `(-3)` next to score, same green/red
- **Days left**: simple count, overwrites on rescan
- **"Alpha Nx"** (consecutive count): replaces "seen N times". Resets to 1 if route changes. Shows for any market with >1 consecutive scan in same route.
- **Route change alert icon**: `⚠` or `↑` when route changed vs previous scan. Clears after 2 scans same group. Tooltip shows specific metrics that changed.
- **NEW badge**: on markets appearing for the first time

Delta baseline: vs the **previous `scan_snapshot`** for this market (could be 2 days ago if market skipped a scan). Always show the timespan: "vs 2d ago".

#### Right Panel — Market Detail (3 tabs)

**[Overview] tab** (default):

```
┌─ Delta vs last scan (2 days ago) ──────────────────┐
│  Price:  $0.28 → $0.32  (+$0.04 ↑14%)             │
│  Score:  71 → 78  (+7)                              │
│  Depth:  $900 → $1.2K  (+$300)                      │
│  Spread: 2.1% → 2.3%  (+0.2%)                      │
│  Days:   20 → 18                                    │
└────────────────────────────────────────────────────┘

┌─ Latest Analysis ───────────────────────────────────┐
│  Conditions: ✓ parsed (Apr 3)                        │
│  Tavily:     ✓ 5 sources (Apr 3)                     │
│  Auditor:    ✓ edge 8.2% (Apr 3)                     │
│  [Re-run Tavily]  [Parse Conditions]  [Audit]        │
└────────────────────────────────────────────────────┘

[... current action sections: prompts, paste areas, import ...]
```

- Delta block only appears if a previous snapshot exists
- "Latest Analysis" shows the most recent event of each type with its date
- All action buttons save results to DB as events (no behavior change, just persistence)
- For watched markets: small volume activity section showing trade_flow_summary data as a mini bar chart (24h view) — not individual rows, just a visual summary

**[Timeline] tab** — full research history:

```
── April 3, 2026 ──────────────────────────────────────

  14:32  AUDIT    edge 8.2%, prob 0.40
                  "Strong YES, one resolution trap..."   [▼]

  14:20  TAVILY   5 sources found
                  "Senate committee vote scheduled..."   [▼]

  11:45  WATCH    spread 2.1% → 4.3% (threshold)        [▼]

  09:15  SCAN     score 78, alpha 4×, YES @ $0.32
                  Tier A · θ 0.85 · depth $1.2K

── April 2, 2026 ──────────────────────────────────────

  10:41  SCAN     score 71, alpha 3×, YES @ $0.28
                  ⚠ was shadow — upgraded: depth $400 → $5K

── April 1, 2026 ──────────────────────────────────────

  09:22  SCAN     score 65, shadow, YES @ $0.25
                  Shadow: low depth ($400) · first seen
```

- `SCAN` = scan_snapshot (full scan or Alpha History Job)
- `WATCH` = watchlist_snapshot (only shown when something triggered it — heartbeat snapshots collapsed by default unless expanded)
- `trade_flow_summary` events: **not shown as individual rows** — aggregated into the mini volume chart in Overview tab
- Alternating row colors for readability
- Each row collapsed to one line, expandable to full payload
- Newest date first, events within a day newest-first

**[Live] tab** — real-time CLOB monitor (only for watched markets, works as today)

#### Watchlist Tab

- Always populated from DB even without scanning
- Shows "SOLVED" label on resolved markets, polling stopped
- Route change indicator visible here too

#### Scan Results Display

- Alpha/Shadow tabs show **fresh scan results only** (current scan)
- Returning markets enriched with: delta badge, "Alpha Nx" consecutive count, route change alert if applicable
- New markets show "NEW" badge
- Watch tab always shows persistent watchlist from DB

---

### Future: Alert Improvements (Deferred — Ideas Only)

Not in scope for this item. Notes for later:

- **Baseline comparison not raw thresholds**: "Spread doubled in 30 min: 2.1% → 4.2%" not "Spread is 4.2%"
- **Aggregated flow patterns**: "3 buys totaling $2.1K in 10 min" not single trade events
- **Depth relative to position size**: "Depth fell below your minimum trade size ($1K)"
- **Price momentum**: "Price moved 10% in 2h — 4× faster than 7-day average"
- **Alert severity tiers**: informational / warning / critical — Telegram only pings on critical
- **Alert deduplication**: same alert type has per-rule cooldown window (already partially there)
- The `trade_flow_summary` events and `watchlist_snapshot` heartbeats written in this item are the baseline data these improved alerts will use

---

## 1. Perplexity API — "Research" Button

**Status:** Not started
**Effort:** Small (1-2 hours)
**Cost:** ~$1/1000 searches (sonar), ~$5/1000 (sonar-pro)

### What we have now
- A detailed `ResearchPrompt()` in `research.go` that generates category-specific research prompts with 7-9 targeted questions, cross-verification rules, historical calibration requirements, and structured JSON output format
- Manual workflow: copy the prompt → paste into Perplexity web → copy the result back

### What we need
- `internal/perplexity/client.go` — HTTP client for Perplexity's Sonar API
  - Endpoint: `POST https://api.perplexity.ai/chat/completions`
  - Model: `sonar` for routine research, `sonar-pro` for deep dives
  - Sonar is Perplexity's API product line — it's their models with built-in web search and citation. You send a prompt, it searches the internet, synthesizes the results with source citations, and returns a structured answer. Unlike raw LLMs, every claim comes with a URL source.
- `POST /api/perplexity/{id}` handler — sends the already-built `ResearchPrompt(sig)` to Sonar API
- UI: Single "Research" button in the signal detail panel → result displayed in a text area
- Store the raw result on the session so the Claude Auditor prompt can reference it

### Why it matters
This is the single highest-ROI improvement. The prompt is already excellent — it generates category-specific factual research questions with cross-verification rules. Right now the bottleneck is the 5-10 minutes of manual copy-paste per market. Automating this turns a 10-minute manual step into a 1-click, 5-second operation.

### API details
- Auth: `Authorization: Bearer <PPLX_API_KEY>` header
- Request body follows OpenAI chat completions format
- Response includes `citations` array with source URLs
- Add `PPLX_API_KEY` to `.env`

---

## 2. Exa.ai — Semantic Research Search

**Status:** Not started
**Effort:** Small-Medium (2-3 hours)
**Cost:** Free tier: 1000 searches/month, then $1/1000

### What we have now
- Tavily for news search (keyword-based, news-focused, good for breaking events)
- Grok for X/Twitter social sentiment

### What we need
- `internal/exa/client.go` — HTTP client for Exa's search API
  - Endpoint: `POST https://api.exa.ai/search`
  - Use `type: "auto"` (neural + keyword hybrid) for best results
  - Use `category: "research paper"` or `"news"` depending on market type
  - `contents: { text: { maxCharacters: 1000 } }` to get snippet content
- Run Tavily + Exa in parallel (Go `errgroup`) when user clicks "Search News"
- UI: Rename existing button from "Search" to "Search News" → results shown in two grouped sections: "Tavily (News)" and "Exa (Research)"

### Why it matters
Tavily and Exa find fundamentally different content:
- **Tavily** excels at recent news articles, breaking events, mainstream coverage. It's keyword-based — finds what matches the words in your query.
- **Exa** uses neural/semantic search — finds content by meaning and intent. It surfaces expert analysis, research reports, niche blogs, academic papers, and long-form analysis that rank poorly in Google/Tavily because they don't optimize for SEO.

For prediction markets, the alpha is often in the expert analysis piece that Tavily misses because it's on a niche domain with no SEO. Exa finds it because the content semantically matches "analysis of whether X will happen."

Additionally, we should feed category-specific specialist domains into both Tavily and Exa as `IncludeDomains` hints:
- Politics: `538.com`, `silverBulletin.com`, `electionbettingodds.com`
- Crypto: `theblock.co`, `messari.io`, `glassnode.com`
- Sports: `fbref.com`, `transfermarkt.com`, `understat.com`

These aren't exclusive filters — just signals to the search engines to prioritize these sources when they appear. Already partially implemented in `categorySearchOptions()` in `tavily.go`, needs extending.

### API details
- Auth: `x-api-key: <EXA_API_KEY>` header
- Request: `{ "query": "...", "type": "auto", "numResults": 10, "contents": { "text": { "maxCharacters": 1000 } } }`
- Response: `{ "results": [{ "title", "url", "text", "publishedDate", "score" }] }`
- Add `EXA_API_KEY` to `.env`

---

## 3. Content Scoring Pipeline — Recency, Authority, Relevance

**Status:** Not started
**Effort:** Small-Medium (2-3 hours)
**Cost:** ~$0.001 per market (Claude Haiku for relevance pass)

### What we have now
- Tavily returns 10 results, we filter by score ≥ 0.40 (Tavily's own relevance metric)
- Category-level `days` filter (7-14 days depending on category)
- All surviving results are treated equally — no ranking by actual usefulness

### What we need
A single composite score computed from three weighted signals. **Nothing gets cut by recency or authority alone** — they only adjust the final ranking. Only the LLM relevance score can eliminate a result.

**Signal 1: Recency weight (soft decay, never cutoff)**
- Parse `publishedDate` from each result, apply a decay multiplier:
  ```
  < 6 hours:    1.0x
  6-24 hours:   0.9x
  1-3 days:     0.7x
  3-7 days:     0.5x
  7-14 days:    0.3x
  14-30 days:   0.1x
  > 30 days:    hard cutoff (only case where we drop)
  ```
- A foundational 5-day-old analysis scoring 9/10 relevance still ranks at 4.5 — which may beat fresh fluff. But fresh high-quality content naturally surfaces to the top.
- Hard cutoff at 24h is wrong — Polymarket reacts fast, but a deep analysis from 3 days ago might contain the key insight that everyone else missed. Soft decay gives preference to fresh data without killing valuable older content.

**Signal 2: Authority boost (additive, never subtractive)**
- Maintain a per-category list of high-authority domains:
  - Politics: 538, Silver Bulletin, ElectionBettingOdds, Reuters, AP, Bloomberg
  - Crypto: CoinDesk, The Block, Messari, Glassnode, Delphi Digital
  - Macro: Fed website, BLS, Bloomberg, Reuters, WSJ
  - Sports: ESPN, BBC Sport, Transfermarkt, FBref, Opta
  - Corporate: SEC EDGAR, Reuters, Bloomberg, FT
- Whitelisted domain: **+1.5** added to final score
- Unknown domain: **+0** (no penalty, ever)
- A brilliant analysis from `randomguy.substack.com` scoring 9/10 relevance (final: 9.0) still outranks a mediocre Reuters article scoring 6/10 relevance (final: 7.5). The whitelist only breaks ties and gives a slight edge when quality is equal.

**Signal 3: LLM relevance scoring — BATCH, not per-snippet**
- Send ALL snippets in a single Claude Haiku call (not 20 individual calls):
  ```
  You are a ruthless research filter for prediction markets.
  Most of these snippets are noise. Only rate above 7 if the text
  contains a specific date, name, number, or event that directly
  affects the market's resolution.

  Market question: "[market question]"

  Rate each snippet 1-10:
  1. "[title + first 200 chars of snippet 1]"
  2. "[title + first 200 chars of snippet 2]"
  ...
  20. "[title + first 200 chars of snippet 20]"

  Return JSON only: {"scores": [7, 3, 9, ...]}
  ```
- One API call (~2000 tokens in, ~50 tokens out) instead of 20 calls. ~$0.0003 total, sub-second latency.
- This is the only score that can eliminate a result (raw relevance < 5 = drop)

**Final composite score:**
```
final_score = (relevance_score + authority_boost) * recency_multiplier
```
- Sort by `final_score` descending
- Drop anything where raw `relevance_score < 5` (noise regardless of source/recency)
- Keep top 8-10 results for the auditor
- This runs automatically behind the scenes — the user sees only the ranked results

### Why it matters
Right now 3-4 out of 10 search results are noise — tangentially related articles that mention the entity but contain no predictive information. The auditor (whether human or Claude) wastes time reading irrelevant content. The composite scoring approach handles this without accidentally cutting valuable content:
- **Recency decay** naturally deprioritizes stale info without killing deep analysis
- **Authority boost** gently surfaces trusted sources without penalizing unknown ones
- **LLM relevance** is the only hard gate — only truly irrelevant content gets dropped

This is especially important for the iterative loop (improvement #5) — if the loop evaluates noisy data, it can't tell what's actually missing vs what's just low-quality.

---

## 4. Cross-Market Price Comparison

**Status:** Not started
**Effort:** Medium (3-5 hours)
**Cost:** Free (public APIs)

### What we have now
- Polymarket prices only — we see what Polymarket thinks but not what other markets think

### What we need
- `internal/kalshi/client.go` — fetch markets from Kalshi public API
  - `GET https://api.elections.kalshi.com/trade-api/v2/markets` (public, no auth for reading)
  - Returns market title, yes_price, no_price, volume, status
- `internal/predictit/client.go` — fetch markets from PredictIt API
  - `GET https://www.predictit.org/api/marketdata/all/` (public JSON, no auth)
  - Returns market name, contracts with prices
- **Market matching:** This is the hard part. We can't reuse the Polymarket scanner directly because each platform phrases questions differently. Approach:
  - When viewing a signal, "Check Other Markets" button
  - Extract key entities from the market question (reuse `extractEntity()`)
  - Search Kalshi/PredictIt for those entities via keyword matching
  - Show matches with their prices side-by-side
  - User manually confirms which matches are the same event
- **Phase 1 (build now):** "Compare Markets" button in the signal detail panel → shows a table per signal:
  ```
  Platform    | YES    | NO     | Volume  | Delta
  Polymarket  | $0.35  | $0.65  | $45K    | —
  Kalshi      | $0.42  | $0.58  | $12K    | +$0.07
  PredictIt   | $0.38  | $0.62  | $8K     | +$0.03
  ```
- **Phase 2 (after Condition Parser):** Pull all Kalshi + PredictIt markets at scan time and surface the delta as a sortable column in the signal list — so a large price gap becomes a discovery signal, not just a confirmation. A 20¢ delta between platforms is one of the strongest alpha signals available and should be visible during market selection, not after.

### Why it matters
Price divergence between platforms is one of the strongest alpha signals available:
- If Kalshi prices YES at $0.55 and Polymarket at $0.35, someone on Kalshi knows something — or Polymarket is mispriced. Either way, that $0.20 gap is information.
- Convergence (all platforms agree) = market is efficient, less edge available
- Divergence = someone is wrong, and finding who is the alpha

This is especially powerful for politics and macro markets where different platforms attract different trader demographics (Kalshi skews institutional, PredictIt skews political junkies, Polymarket skews crypto-native).

### API details
- Kalshi: Public read API, rate limit ~10 req/s. Markets have `ticker` field searchable by keyword.
- PredictIt: Single JSON dump of all markets. Fetch once, search in-memory. ~300KB response.
- No auth needed for either — both are public read endpoints.

---

## 5. Iterative Research Loop — Self-Improving Analysis

**Status:** Not started
**Effort:** Medium (4-6 hours)
**Cost:** ~$0.05-0.10 per market (2-3 LLM calls)

### What we have now
- Single-pass research: search once → show results → done
- No evaluation of whether the research actually answered the question
- No follow-up on gaps

### What we need
Build a research loop in pure Go (no frameworks needed):

```
Step 1: Initial Research
  - Run Perplexity + Tavily + Exa + Grok in parallel
  - Collect all results, apply relevance scoring

Step 2: Gap Analysis (LLM call)
  - Send all collected evidence to Claude Haiku with a strict prompt:
    "Given this evidence about [question], list ONLY facts that:
     (a) would be findable via public web search, AND
     (b) would change the prediction by more than 10 percentage points.
     If the research covers the key factors, reply: SUFFICIENT."
  - If LLM says "SUFFICIENT" → skip to step 4

Step 3: ONE Targeted Follow-up (never more)
  - Convert each identified gap into a search query
  - Run those queries through Tavily + Exa
  - Apply batch relevance scoring to new results
  - Merge with existing evidence
  - Do NOT loop back to step 2 — two passes is the hard maximum

Step 4: Final Synthesis
  - All collected evidence is available for the Claude Auditor prompt
  - Include a "research completeness" flag (gaps filled / gaps identified)

Max iterations: 2 total (initial search + one follow-up). If info isn't found
in two passes, it's not publicly available. LLMs will hallucinate "gaps" to
be helpful — the strict prompt and hard cap prevent runaway costs.
```

- UI: The "Research" button shows progress: "Searching... → Analyzing gaps... → Deep diving... → Complete"
- Store the full research chain (initial findings + gaps identified + follow-up findings) for the auditor

### Why it matters
Single-pass research misses things. A human researcher naturally does this loop — they read results, notice "wait, I don't know X yet", and search again. The key insight is that LLMs are good at identifying what's missing from a body of evidence. The gap analysis step costs ~$0.01 and often catches critical missing pieces:

- "No data found on the electoral system mechanics — is this a first-past-the-post or proportional system?"
- "Multiple sources mention a court ruling but none give the date — when is the ruling expected?"
- "Polling data is from 2 weeks ago — are there more recent polls?"

Without the loop, these gaps silently flow into the auditor and degrade prediction quality.

### Implementation notes
- No LangGraph/CrewAI needed — this is straightforward Go: goroutines for parallel search, sequential LLM calls for evaluation
- Go's `errgroup` handles the parallel search step cleanly
- The evaluation prompt is the "brain" — spend time making it good
- Budget control: track token usage per iteration, hard stop at $0.15 per market

---

## 6. LLM-Based Condition Parser — Resolution Trap Detection

**Status:** Not started
**Effort:** Medium (3-4 hours)
**Cost:** ~$0.005 per market (Claude Haiku)

### What we have now
- Rule-based `extractEntity()` in `research.go` — handles ~80% of markets
- `categoryQuestions()` generates good questions but doesn't deeply analyze resolution criteria
- The market `description` field (Polymarket's resolution rules) is included in the research prompt but not separately analyzed

### What we need
- When a market is selected, send its `description` (resolution criteria) to Claude Haiku with:
  ```
  Analyze this prediction market's resolution criteria:

  Question: "[question]"
  Resolution rules: "[description]"

  Extract:
  1. EXACT trigger conditions (what specifically must happen for YES/NO)
  2. Resolution source (who/what determines the outcome)
  3. Edge cases (scenarios where the event happens but market resolves differently)
  4. Key dates (deadlines, cutoffs embedded in the rules)
  5. Ambiguity risk (low/medium/high) — could reasonable people disagree on resolution?

  Return as JSON.
  ```
- Display the parsed conditions in the signal detail panel as a small info block
- Feed the parsed conditions into the research prompt for more targeted questions

### Why it matters
Resolution traps are where prediction market traders lose money even when they're right about the underlying event. Examples:
- "Will X win the election?" — but the market resolves based on certified results, not projected winner. A legal challenge could delay certification past the resolution date → resolves NO even though X won.
- "Will Bitcoin hit $100K?" — but the resolution criteria specify "as reported by CoinGecko at 00:00 UTC on [date]". A flash crash or wick that hits $100K on Binance but not CoinGecko → resolves NO.
- "Will the bill pass?" — but "pass" means signed into law, not just voted through one chamber.

The current rule-based parser can't catch these. An LLM reading the fine print can.

### Priority
Build right after Perplexity API. Without understanding resolution rules, every downstream step (research, cross-market comparison, auditor) is working with incomplete context. The parsed conditions feed into everything: they improve research queries, they make cross-market matching safe, and they catch resolution traps before you bet money on a market you misunderstand.

---

## 7. Grok X Search — Expert Handle Targeting

**Status:** Enhancement to existing feature
**Effort:** Small (1 hour)
**Cost:** No additional cost (same Grok API calls)

### What we have now
- Grok `x_search` searches by market question keywords — "Will Bitcoin hit $100K?" → searches X for "Bitcoin $100K"

### What we need
- Maintain a per-category list of niche opinion leaders / expert handles:
  - Politics: `@NateSilver538`, `@Redistrict`, `@PollTrackerUSA`, `@ElectsWorld`
  - Crypto: `@lookonchain`, `@WhaleFud`, `@DefiIgnas`, `@CryptoQuant_com`
  - Macro: `@NickTimiraos`, `@FedGuy12`, `@MacroAlf`
  - Sports: `@OptaJoe`, `@Transfermarkt`, `@FBref`
- Update `buildXSearchPrompt()` to include: "Also specifically search for posts from these expert accounts: [handles]. These are domain experts whose opinions carry more weight than general discussion."
- Keep the event keyword search too — it finds *what happened* (breaking news, official announcements). Expert handles find *what it means* (interpretation, insider context, predictive analysis). Both are needed.
- Expert handle targeting is X-specific only. Tavily/Exa search web content, not social accounts — opinion leaders don't apply there.

### Why it matters
X/Twitter alpha comes from specific people, not the crowd. A random account tweeting "Bitcoin to the moon" is noise. `@lookonchain` reporting whale wallet movements is signal. But you still need event keyword search to catch the breaking news that the experts are reacting to. The combination of "what happened" + "what experts think about it" is where the real edge lives.

---

## Future Considerations (Not Yet Planned)

These items were evaluated and deferred. Revisit after items 1-7 are built and validated.

| Item | Why deferred |
|------|-------------|
| **Serper.dev** (Google Search API) | Tavily + Exa cover this. Adding a third keyword search engine is redundancy without clear uplift. |
| **Firecrawl / Jina Reader** (web scraping) | Only needed for specialized sources (court dockets, SEC EDGAR). The resolution criteria parser (item 6) reduces the need for this by identifying which sources matter before scraping them. |
| **Telegram group monitoring** | Second-best social signal after X. Prediction market Telegram groups (e.g., Polymarket Discord/TG, crypto alpha groups) share real-time trade ideas and insider analysis. Complex to implement (requires bot in groups, message parsing, spam filtering). Build after X sentiment proves valuable — same concept, harder plumbing. |
| **LangGraph / CrewAI** | Python frameworks. We're in Go and the iterative loop (item 5) achieves the same thing in ~50 lines of Go. No framework needed. |
| **Manifold Markets comparison** | Third prediction market alongside Kalshi/PredictIt. Lower volume and less institutional money, so price signals are weaker. Add to cross-market comparison (item 4) if the first two platforms prove useful. |

---

## Build Order (Revised)

| # | Task | Why this order |
|---|------|----------------|
| **0** | **Data Persistence & Timeline** (item 0) | Foundation — everything else depends on data surviving restarts. Watchlist, history, deltas all need SQLite. |
| **1** | **Perplexity API** (item 1) | Kills the biggest manual bottleneck. 2 hours, instant ROI. Results now persist via timeline. |
| **2** | **Condition Parser** (item 6) | Protects against resolution traps. Everything downstream depends on understanding the rules correctly. |
| **3** | **Exa.ai + parallel search** (item 2) | Adds a fundamentally different data layer — expert analysis that keyword search misses. |
| **4** | **Batch content scoring** (item 3) | One Haiku call to filter all snippets. Cheap, fast, prevents noise from reaching the auditor. |
| **5** | **Cross-market comparison** (item 4) | Safe now because condition parser catches rule mismatches across platforms. |
| **6** | **Grok expert handles** (item 7) | Small enhancement to existing X search — zero cost, better signal quality. |
| **7** | **Iterative loop** (item 5) | 1 follow-up pass max. Only after single-pass quality is proven solid. |
