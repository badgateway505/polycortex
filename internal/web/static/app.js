// Golden Rain — Web UI

// renderMd renders the small Markdown subset used by AI summaries:
// **bold**, bullet lines starting with •, and bare newlines → <br>.
function renderMd(text) {
    if (!text) return '';
    return escHtml(text)
        // **bold**
        .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
        // newline after a bullet → close bullet, next bullet starts fresh
        .replace(/\n/g, '<br>');
}
// State
let signals = { alphas: [], shadows: [] };
let selectedMarketID = null;
let currentTab = 'alpha';
let prompts = {}; // source → prompt text
let marketUIState = {}; // marketID → saved UI state (survives market switching)

// Monitor state
let watchlist = [];       // [{market_id, question, enabled_rules, added_at, current_price, trade_count}]
let alertFeedItems = [];  // recent alerts (newest first)
let allRules = [];        // rule catalogue from /api/rules
let ruleModalMarketID = null;
let alertPollTimer = null;

// Live tracking state
let liveTrackingID = null;
let liveTrackTimer = null;
let livePrevValues = {};

// --- Scan ---

async function runScan() {
    const limit = parseInt(document.getElementById('scan-limit').value) || 5000;
    const btn = document.getElementById('btn-scan');
    const status = document.getElementById('scan-status');

    btn.disabled = true;
    status.innerHTML = 'Scanning... <span class="loading"></span>';

    try {
        const resp = await fetch('/api/scan', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ limit })
        });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        signals.alphas = data.alphas || [];
        signals.shadows = data.shadows || [];

        document.getElementById('alpha-count').textContent = signals.alphas.length;
        document.getElementById('shadow-count').textContent = signals.shadows.length;

        status.textContent = `${data.total_scanned} markets in ${data.duration}`;

        renderSignalList();
        clearDetail();
    } catch (err) {
        status.textContent = 'Error: ' + err.message;
    } finally {
        btn.disabled = false;
    }
}

// --- Tabs ---

function showTab(tab) {
    currentTab = tab;
    document.querySelectorAll('.tab').forEach((t, i) => {
        t.classList.toggle('active', (tab === 'alpha' && i === 0) || (tab === 'shadow' && i === 1) || (tab === 'portfolio' && i === 2));
    });
    if (tab === 'portfolio') renderPortfolioList();
    else renderSignalList();
}

// --- Signal List ---

function renderSignalList() {
    const list = document.getElementById('signal-list');
    const items = currentTab === 'alpha' ? signals.alphas : signals.shadows;

    if (!items || items.length === 0) {
        list.innerHTML = `<div style="padding:20px;color:var(--text-dim);text-align:center">No ${currentTab} signals</div>`;
        return;
    }

    list.innerHTML = items.map((sig, i) => {
        const isSelected = sig.market_id === selectedMarketID;
        const sideClass = sig.target_side === 'YES' ? 'side-yes' : 'side-no';
        const isAlpha = sig.is_alpha;

        let indicators = '';
        if (isAlpha) {
            indicators = `
                <div class="indicators">
                    <span class="indicator ${sig.has_tavily ? 'done' : ''}">T</span>
                    <span class="indicator ${sig.has_exa ? 'done' : ''}">E</span>
                    <span class="indicator ${sig.has_perplexity ? 'done' : ''}">P</span>
                    ${sig.pillarlab_enabled ? `<span class="indicator ${sig.has_pillarlab ? 'done' : ''}">L</span>` : ''}
                </div>`;
        }

        let shadowReasons = '';
        if (!isAlpha && sig.shadow_reasons && sig.shadow_reasons.length) {
            shadowReasons = `<div class="reasons">${sig.shadow_reasons.join(', ')}</div>`;
        }

        return `
            <div class="signal-item ${isAlpha ? '' : 'shadow-item'} ${isSelected ? 'selected' : ''}"
                 onclick="selectSignal('${sig.market_id}')">
                <div class="signal-item-header">
                    <div class="question">${escHtml(sig.question)}</div>
                    <button class="btn-debug" title="Copy full pipeline audit prompt for Opus"
                            onclick="event.stopPropagation(); copyDebugPrompt('${sig.market_id}')">🐛</button>
                </div>
                <div class="meta">
                    <span class="score">${sig.score.toFixed(1)}</span>
                    <span class="${sideClass}">${sig.target_side} $${sig.price.toFixed(3)}</span>
                    <span>[${sig.category}]</span>
                    <span>${sig.days_to_resolve}d</span>
                    <span>Tier ${sig.tier}</span>
                </div>
                ${indicators}
                ${shadowReasons}
            </div>`;
    }).join('');
}

// --- Signal Detail ---

function selectSignal(marketID) {
    // Save current market's UI state before switching away
    if (selectedMarketID && selectedMarketID !== marketID) {
        saveMarketState(selectedMarketID);
        // Clear comments so stale data from previous market isn't shown
        commentsData = null;
        const countEl = document.getElementById('comments-count');
        const areaEl = document.getElementById('comments-results');
        const btnEl = document.getElementById('btn-comments');
        if (countEl) countEl.textContent = '';
        if (areaEl) areaEl.innerHTML = '';
        if (btnEl) btnEl.textContent = 'Load';
    }
    selectedMarketID = marketID;
    if (currentTab === 'portfolio') renderPortfolioList();
    else renderSignalList();

    const sig = findSignal(marketID);
    if (!sig) return;

    document.getElementById('no-selection').classList.add('hidden');
    document.getElementById('signal-detail').classList.remove('hidden');

    // Market info
    const sideClass = sig.target_side === 'YES' ? 'side-yes' : 'side-no';
    // Stop live tracking if switching to a different market
    if (liveTrackingID && liveTrackingID !== marketID) stopTracking();

    const isWatched = watchlist.some(w => w.market_id === sig.market_id);
    const isTracking = liveTrackingID === sig.market_id;
    const trackBtn = isWatched
        ? `<button id="btn-track" onclick="toggleTrack('${sig.market_id}')" style="flex-shrink:0;padding:6px 12px;font-size:13px;border:1px solid ${isTracking ? '#f85149' : '#3fb950'};background:${isTracking ? '#da3633' : '#238636'};color:#fff;cursor:pointer;border-radius:4px;font-weight:600">${isTracking ? '■ Stop' : '▶ Track'}</button>`
        : '';
    document.getElementById('market-info').innerHTML = `
        <div style="display:flex;justify-content:space-between;align-items:flex-start;gap:12px">
            <div class="question-full">${escHtml(sig.question)}</div>
            <div style="display:flex;gap:6px;flex-shrink:0">
                <button id="btn-watch" onclick="toggleWatch('${sig.market_id}')" style="padding:6px 16px;font-size:13px;border:1px solid #58a6ff;background:#1f6feb;color:#fff;cursor:pointer;border-radius:4px;font-weight:600">${isWatched ? '★ Watching' : '☆ Watch'}</button>
                ${trackBtn}
            </div>
        </div>
        <div class="trade-direction">
            Buy <span class="${sideClass}" style="font-weight:700">${sig.target_side}</span> @ $${sig.price.toFixed(3)}
            &nbsp;&middot;&nbsp;
            <a href="${sig.url}" target="_blank" style="color:var(--accent)">Polymarket</a>
        </div>
        <div class="stats">
            <div class="stat"><span class="stat-label">VWAP</span> <span class="stat-value">$${sig.vwap.toFixed(4)}</span></div>
            <div class="stat"><span class="stat-label">Depth</span> <span class="stat-value">$${sig.true_depth_usd.toFixed(0)}</span></div>
            <div class="stat"><span class="stat-label">D/V</span> <span class="stat-value">${sig.dv_ratio.toFixed(3)}</span></div>
            <div class="stat"><span class="stat-label">Spread</span> <span class="stat-value">${sig.spread_pct.toFixed(1)}%</span></div>
            <div class="stat"><span class="stat-label">Theta</span> <span class="stat-value">${sig.theta.toFixed(2)} (${sig.days_to_resolve}d)</span></div>
            <div class="stat"><span class="stat-label">Activity</span> <span class="stat-value">${sig.activity}</span></div>
            <div class="stat"><span class="stat-label">Liquidity</span> <span class="stat-value">$${(sig.liquidity/1000).toFixed(0)}K (Tier ${sig.tier})</span></div>
            <div class="stat"><span class="stat-label">Category</span> <span class="stat-value">${sig.category}</span></div>
        </div>`;

    // Restore per-market UI state (or initialize clean state for fresh market)
    restoreMarketState(marketID, sig);

    // Load saved paste data
    loadPaste('perplexity');
    if (sig.pillarlab_enabled) {
        loadPaste('pillarlab');
    }

    // Sync PillarLab toggle and UI elements with config
    const plToggle = document.getElementById('pillarlab-toggle');
    if (plToggle) plToggle.checked = sig.pillarlab_enabled;
    document.querySelectorAll('.pillarlab-ui').forEach(el => {
        el.style.display = sig.pillarlab_enabled ? '' : 'none';
    });

    // Update auditor status
    updateAuditorStatus(sig);
}

function clearDetail() {
    selectedMarketID = null;
    document.getElementById('no-selection').classList.remove('hidden');
    document.getElementById('signal-detail').classList.add('hidden');
}

function findSignal(marketID) {
    return signals.alphas.find(s => s.market_id === marketID)
        || signals.shadows.find(s => s.market_id === marketID);
}

function saveMarketState(marketID) {
    if (!marketID) return;
    const badge = document.getElementById('grok-sentiment-badge');
    marketUIState[marketID] = {
        tavilyHtml:            document.getElementById('tavily-results').innerHTML,
        grokHtml:              document.getElementById('grok-results').innerHTML,
        grokBadgeText:         badge ? badge.textContent : '',
        grokBadgeClass:        badge ? badge.className : 'grok-badge',
        conditionHtml:         document.getElementById('condition-results').innerHTML,
        auditorHtml:           document.getElementById('auditor-api-results').innerHTML,
        auditorStatusHtml:     document.getElementById('auditor-status').innerHTML,
        prompts:               { ...prompts },
        promptText:            document.getElementById('prompt-text').textContent,
        promptVisible:         !document.getElementById('prompt-preview').classList.contains('hidden'),
        copyPerplexityVisible: !document.getElementById('copy-perplexity').classList.contains('hidden'),
        copyPillarlabVisible:  !document.getElementById('copy-pillarlab').classList.contains('hidden'),
    };
}

function restoreMarketState(marketID, sig) {
    const state = marketUIState[marketID];
    if (!state) {
        // Fresh market — initialize to clean state with cache indicators
        document.getElementById('tavily-results').innerHTML = (sig.has_tavily || sig.has_exa)
            ? '<span style="color:var(--green);font-size:12px">Cached from previous search</span>'
            : '';
        const badge = document.getElementById('grok-sentiment-badge');
        if (badge) { badge.textContent = ''; badge.className = 'grok-badge'; }
        document.getElementById('grok-results').innerHTML = sig.has_grok
            ? '<span style="color:var(--green);font-size:12px">Cached from previous search</span>'
            : '';
        document.getElementById('condition-results').innerHTML = '';
        document.getElementById('auditor-api-results').innerHTML = '';
        document.getElementById('prompt-preview').classList.add('hidden');
        document.querySelectorAll('.copy-btn').forEach(b => b.classList.add('hidden'));
        prompts = {};
        return;
    }
    // Restore saved state for this market
    document.getElementById('tavily-results').innerHTML    = state.tavilyHtml;
    document.getElementById('grok-results').innerHTML     = state.grokHtml;
    document.getElementById('condition-results').innerHTML = state.conditionHtml;
    document.getElementById('auditor-api-results').innerHTML = state.auditorHtml;
    document.getElementById('auditor-status').innerHTML   = state.auditorStatusHtml;
    const badge = document.getElementById('grok-sentiment-badge');
    if (badge) { badge.textContent = state.grokBadgeText; badge.className = state.grokBadgeClass; }
    prompts = { ...state.prompts };
    const preview = document.getElementById('prompt-preview');
    if (state.promptVisible && state.promptText) {
        document.getElementById('prompt-text').textContent = state.promptText;
        preview.classList.remove('hidden');
    } else {
        preview.classList.add('hidden');
    }
    const copyPerp = document.getElementById('copy-perplexity');
    if (copyPerp) copyPerp.classList.toggle('hidden', !state.copyPerplexityVisible);
    const copyPL = document.getElementById('copy-pillarlab');
    if (copyPL) copyPL.classList.toggle('hidden', !state.copyPillarlabVisible);
}

// --- News Search (AI-generated queries → Tavily + Exa in parallel) ---

async function runNewsSearch() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-tavily');
    const area = document.getElementById('tavily-results');

    btn.disabled = true;
    area.innerHTML = 'Generating queries... <span class="loading"></span>';

    try {
        const resp = await fetch(`/api/news/${selectedMarketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        let html = '';

        // Show AI-generated queries so user can see what was searched
        if (data.queries) {
            html += `<div class="search-queries">
                <div><span class="query-label">Tavily:</span> ${escHtml(data.queries.tavily)}</div>
                <div><span class="query-label">Exa:</span> ${escHtml(data.queries.exa)}</div>
            </div>`;
        }

        // AI summary of key facts
        if (data.summary) {
            html += `<div class="news-summary">${renderMd(data.summary)}</div>`;
        }

        // Scored results — unified, filtered, sorted by composite score
        if (data.scored && data.scored.length) {
            html += `<div class="search-section-header">Sources (${data.scored.length} results, ranked by relevance)</div>`;

            // Exa insights lookup — indexed against scored Exa results order,
            // which matches the order articles were passed to DigestArticles.
            const insights = (data.exa && data.exa.insights) || [];
            const insightByTitle = {};
            let exaIdx = 0;
            data.scored.forEach(r => {
                if (r.source === 'exa') {
                    exaIdx++;
                    const ins = insights.find(x => x.index === exaIdx);
                    if (ins && ins.insight !== 'NOT RELEVANT') {
                        insightByTitle[r.title] = ins.insight;
                    }
                }
            });

            html += data.scored.map(r => {
                const scoreColor = r.composite_score >= 7 ? 'var(--green)'
                    : r.composite_score >= 4 ? 'var(--accent)' : 'var(--muted)';
                const sourceBadge = r.source === 'exa' ? 'EXA' : 'TAV';
                const insight = insightByTitle[r.title];
                const dateStr = r.published_date || '';
                // For Tavily, show snippet (its text is already a clean extract).
                // For Exa without a digest, skip snippet — Exa's raw text is often
                // navigation boilerplate and adds no value without AI extraction.
                const body = insight
                    ? `<div class="article-insight">${escHtml(insight)}</div>`
                    : r.source !== 'exa'
                        ? `<div class="snippet">${escHtml((r.text || '').substring(0, 200))}</div>`
                        : '';
                return `
                <div class="tavily-item">
                    <div class="scored-header">
                        <span class="score-badge" style="color:${scoreColor}">${r.composite_score.toFixed(1)}</span>
                        <span class="source-badge source-${r.source}">${sourceBadge}</span>
                        <span class="title"><a href="${escHtml(r.url)}" target="_blank">${escHtml(r.title)}</a></span>
                    </div>
                    ${dateStr ? `<div class="date">${escHtml(dateStr)}</div>` : ''}
                    ${body}
                </div>`;
            }).join('');

            if (data.tavily && !data.tavily.error) updateSignalIndicator(selectedMarketID, 'has_tavily', true);
            if (data.exa && !data.exa.error) updateSignalIndicator(selectedMarketID, 'has_exa', true);
        } else {
            // Fallback: show raw results if scoring failed
            html += '<div class="search-section-header">Tavily (News)</div>';
            const tv = data.tavily;
            if (tv && !tv.error && tv.results && tv.results.length) {
                html += tv.results.map(r => `
                    <div class="tavily-item">
                        <div class="title"><a href="${escHtml(r.url)}" target="_blank">${escHtml(r.title)}</a></div>
                        ${r.published_date ? `<div class="date">${escHtml(r.published_date)}</div>` : ''}
                        <div class="snippet">${escHtml((r.content || '').substring(0, 300))}</div>
                    </div>`).join('');
                updateSignalIndicator(selectedMarketID, 'has_tavily', true);
            } else {
                html += `<div style="color:var(--muted);font-size:12px">${tv?.error || 'No results.'}</div>`;
            }

            html += '<div class="search-section-header" style="margin-top:12px">Exa (Research)</div>';
            const ex = data.exa;
            if (ex && !ex.error && ex.results && ex.results.length) {
                html += ex.results.map(r => `
                    <div class="tavily-item">
                        <div class="title"><a href="${escHtml(r.url)}" target="_blank">${escHtml(r.title)}</a></div>
                        ${r.publishedDate ? `<div class="date">${escHtml(r.publishedDate)}</div>` : ''}
                        <div class="snippet">${escHtml((r.text || '').substring(0, 200))}</div>
                    </div>`).join('');
                updateSignalIndicator(selectedMarketID, 'has_exa', true);
            } else {
                html += `<div style="color:var(--muted);font-size:12px">${ex?.error || 'No results.'}</div>`;
            }
        }

        area.innerHTML = html;
        updateAuditorStatus(findSignal(selectedMarketID));
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
    } finally {
        btn.disabled = false;
    }
}

// --- Grok X Sentiment ---

async function runGrok() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-grok');
    const area = document.getElementById('grok-results');
    const badge = document.getElementById('grok-sentiment-badge');

    btn.disabled = true;
    area.innerHTML = 'Searching X... <span class="loading"></span>';
    badge.textContent = '';

    try {
        const resp = await fetch(`/api/grok/${selectedMarketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        area.innerHTML = renderGrokInsight(data);
        badge.className = 'grok-badge grok-' + (data.sentiment || 'unknown');
        badge.textContent = data.sentiment || '';

        updateSignalIndicator(selectedMarketID, 'has_grok', true);
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
    } finally {
        btn.disabled = false;
    }
}

function renderGrokInsight(d) {
    let html = '';

    if (d.summary) {
        html += `<div class="grok-summary">${escHtml(d.summary)}</div>`;
    }

    const hasPoints = (d.bull_points && d.bull_points.length) || (d.bear_points && d.bear_points.length);
    if (hasPoints) {
        html += `<div class="grok-points-row">`;
        if (d.bull_points && d.bull_points.length) {
            html += `<div class="grok-points grok-bull">
                <div class="grok-points-label">Bull</div>
                <ul>${d.bull_points.map(p => `<li>${escHtml(p)}</li>`).join('')}</ul>
            </div>`;
        }
        if (d.bear_points && d.bear_points.length) {
            html += `<div class="grok-points grok-bear">
                <div class="grok-points-label">Bear</div>
                <ul>${d.bear_points.map(p => `<li>${escHtml(p)}</li>`).join('')}</ul>
            </div>`;
        }
        html += `</div>`;
    }

    if (d.key_themes && d.key_themes.length) {
        html += `<div class="grok-themes">`;
        html += d.key_themes.map(t => `<span class="grok-tag">${escHtml(t)}</span>`).join('');
        html += `</div>`;
    }

    if (d.hashtags && d.hashtags.length) {
        html += `<div class="grok-themes">`;
        html += d.hashtags.map(h => `<span class="grok-tag grok-hashtag">${escHtml(h)}</span>`).join('');
        html += `</div>`;
    }

    if (d.notable_posts && d.notable_posts.length) {
        html += `<div class="grok-posts">`;
        html += d.notable_posts.map(p => `
            <div class="grok-post">
                <div class="grok-post-header">
                    <span class="grok-post-author">${escHtml(p.author)}</span>
                    ${p.url ? `<a href="${escHtml(p.url)}" target="_blank" class="grok-post-link">↗</a>` : ''}
                    <span class="grok-post-why">${escHtml(p.why)}</span>
                </div>
                <div class="grok-post-content">${escHtml(p.content)}</div>
            </div>`).join('');
        html += `</div>`;
    }

    if (d.searched_at) {
        const ts = new Date(d.searched_at).toLocaleTimeString();
        html += `<div class="result-timestamp">Searched at ${ts}</div>`;
    }

    return html;
}

// --- Condition Parser ---

async function parseConditions() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-condition');
    const area = document.getElementById('condition-results');

    btn.disabled = true;
    area.innerHTML = 'Analyzing resolution criteria... <span class="loading"></span>';

    try {
        const resp = await fetch(`/api/condition/${selectedMarketID}`, { method: 'POST' });
        const data = await resp.json();

        if (data.error) {
            throw new Error(data.error);
        }

        let html = '';

        // Helper: format field value — handles newlines and JSON arrays/objects
        function fmtCondition(val) {
            if (!val) return '';
            let s = val;
            // If it looks like a JSON array, parse and join with newlines
            if (s.startsWith('[')) {
                try {
                    const arr = JSON.parse(s);
                    s = arr.join('\n');
                } catch(e) { /* use as-is */ }
            }
            // If it looks like a JSON object, format key: value lines
            if (s.startsWith('{')) {
                try {
                    const obj = JSON.parse(s);
                    s = Object.entries(obj).map(([k,v]) => k.toUpperCase() + ': ' + v).join('\n');
                } catch(e) { /* use as-is */ }
            }
            // Convert newlines to <br> for display
            return escHtml(s).replace(/\n/g, '<br>');
        }

        // Render parsed conditions
        if (data.trigger_conditions) {
            html += `<div class="condition-block">
                <div class="condition-label">Trigger Conditions</div>
                <div class="condition-text">${fmtCondition(data.trigger_conditions)}</div>
            </div>`;
        }

        if (data.resolution_source) {
            html += `<div class="condition-block">
                <div class="condition-label">Resolution Source</div>
                <div class="condition-text">${fmtCondition(data.resolution_source)}</div>
            </div>`;
        }

        if (data.edge_cases) {
            html += `<div class="condition-block">
                <div class="condition-label">Edge Cases (Traps)</div>
                <div class="condition-text">${fmtCondition(data.edge_cases)}</div>
            </div>`;
        }

        if (data.key_dates) {
            html += `<div class="condition-block">
                <div class="condition-label">Key Dates</div>
                <div class="condition-text">${fmtCondition(data.key_dates)}</div>
            </div>`;
        }

        if (data.ambiguity_risk) {
            const risk = data.ambiguity_risk.toLowerCase().trim();
            const riskClass = 'risk-' + risk;
            html += `<div class="condition-block">
                <div class="condition-label">Ambiguity Risk</div>
                <div class="condition-risk ${riskClass}">${escHtml(risk).toUpperCase()}</div>
            </div>`;
        }

        area.innerHTML = html || 'No condition data found.';
        updateSignalIndicator(selectedMarketID, 'has_condition_parser', true);
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
    } finally {
        btn.disabled = false;
    }
}

// --- Perplexity API Research ---

async function runPerplexityResearch() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-pplx-research');
    const status = document.getElementById('pplx-research-status');
    const textarea = document.getElementById('paste-perplexity');

    btn.disabled = true;
    status.textContent = 'Fetching...';

    try {
        const resp = await fetch(`/api/perplexity/${selectedMarketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        // Auto-fill the textarea with the result (citations appended)
        let text = data.text || '';
        if (data.citations && data.citations.length) {
            text += '\n\n--- CITATIONS ---\n';
            data.citations.forEach((c, i) => { text += `[${i + 1}] ${c}\n`; });
        }
        textarea.value = text;

        // Update indicators — result is already saved in session by the backend
        updateSignalIndicator(selectedMarketID, 'has_perplexity', true);
        updateAuditorStatus(findSignal(selectedMarketID));

        status.textContent = `Done (${data.model})`;
        setTimeout(() => { status.textContent = ''; }, 4000);
    } catch (err) {
        status.textContent = 'Error: ' + err.message;
        setTimeout(() => { status.textContent = ''; }, 6000);
    } finally {
        btn.disabled = false;
    }
}

// --- Prompts ---

async function getPrompt(source) {
    if (!selectedMarketID) return;
    const endpoint = `/api/prompt/${source}/${selectedMarketID}`;

    try {
        const resp = await fetch(endpoint);
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        prompts[source] = data.prompt;

        // Show preview
        const preview = document.getElementById('prompt-preview');
        const text = document.getElementById('prompt-text');
        text.textContent = data.prompt;
        preview.classList.remove('hidden');

        // Show copy button
        const copyBtn = document.getElementById(`copy-${source}`);
        if (copyBtn) copyBtn.classList.remove('hidden');
    } catch (err) {
        alert('Error: ' + err.message);
    }
}

async function copyPrompt(source) {
    const text = prompts[source];
    if (!text) return;

    try {
        await navigator.clipboard.writeText(text);
        const btn = document.getElementById(`copy-${source}`);
        const orig = btn.textContent;
        btn.textContent = 'Copied!';
        setTimeout(() => btn.textContent = orig, 1500);
    } catch (err) {
        // Fallback for non-HTTPS
        const ta = document.createElement('textarea');
        ta.value = text;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
    }
}

// --- Paste ---

async function savePaste(source) {
    if (!selectedMarketID) return;
    const textarea = document.getElementById(`paste-${source}`);
    const data = textarea.value.trim();
    if (!data) return;

    try {
        await fetch(`/api/paste/${source}/${selectedMarketID}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ data })
        });
        const indicator = document.getElementById(`saved-${source}`);
        indicator.textContent = 'Saved';
        setTimeout(() => indicator.textContent = '', 2000);

        updateSignalIndicator(selectedMarketID, `has_${source}`, true);
        updateAuditorStatus(findSignal(selectedMarketID));
    } catch (err) {
        alert('Error saving: ' + err.message);
    }
}

async function loadPaste(source) {
    if (!selectedMarketID) return;
    try {
        const resp = await fetch(`/api/paste/${source}/${selectedMarketID}`);
        const data = await resp.json();
        document.getElementById(`paste-${source}`).value = data.data || '';
    } catch (e) {
        // Ignore
    }
}

// --- Auditor Status ---

function updateAuditorStatus(sig) {
    if (!sig) return;
    const el = document.getElementById('auditor-status');
    const tavily = sig.has_tavily;
    const perp = sig.has_perplexity;
    const pillar = sig.has_pillarlab;

    let html = `
        <span class="${tavily ? 'filled' : 'missing'}">${tavily ? '&#10003;' : '&#10007;'} Tavily</span>&nbsp;&nbsp;
        <span class="${perp ? 'filled' : 'missing'}">${perp ? '&#10003;' : '&#10007;'} Perplexity</span>`;
    if (sig.pillarlab_enabled) {
        html += `&nbsp;&nbsp;<span class="${pillar ? 'filled' : 'missing'}">${pillar ? '&#10003;' : '&#10007;'} PillarLab</span>`;
    }
    el.innerHTML = html;
}

// --- PillarLab Toggle ---

async function togglePillarlab(enabled) {
    try {
        await fetch('/api/config/pillarlab', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({enabled})
        });
        // Refresh signal list to pick up new pillarlab_enabled flag
        await loadSignals();
        if (selectedMarketID) {
            const sig = allSignals.find(s => s.market_id === selectedMarketID);
            if (sig) {
                document.querySelectorAll('.pillarlab-ui').forEach(el => {
                    el.style.display = enabled ? '' : 'none';
                });
                updateAuditorStatus(sig);
            }
        }
    } catch (e) {
        console.error('Failed to toggle PillarLab:', e);
    }
}

// --- Claude Auditor API ---

async function runAuditor() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-run-auditor');
    const area = document.getElementById('auditor-api-results');

    btn.disabled = true;
    btn.textContent = 'Running...';
    area.innerHTML = '<span class="loading"></span> Calling Claude Opus — this takes 1-5 minutes...';

    try {
        const resp = await fetch(`/api/audit/${selectedMarketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        if (!data.matched || data.matched.length === 0) {
            area.innerHTML = '<span style="color:var(--yellow)">No matches — check market ID in Claude response.</span>';
            return;
        }

        area.innerHTML = data.matched.map(m => {
            const isPositive = m.edge > 0;
            let html = `
                <div class="edge-result ${isPositive ? 'edge-positive' : 'edge-negative'}">
                    <div class="edge-value" style="color:${isPositive ? 'var(--green)' : 'var(--red)'}">
                        ${isPositive ? '+' : ''}${m.edge_pct.toFixed(1)}% edge
                    </div>
                    <div class="edge-detail">
                        ${m.our_side} @ $${m.our_price.toFixed(3)} | ${probLine(m.true_prob, m.our_side)} | ${m.confidence}
                    </div>
                    <div class="edge-detail" style="margin-top:4px">${escHtml(m.reasoning || '')}</div>
                </div>`;

            if (m.uncertainty_sources && m.uncertainty_sources.length > 0) {
                html += renderUncertaintySources(m.uncertainty_sources, m.market_id);
            }

            return html;
        }).join('');

        if (data.warnings && data.warnings.length > 0) {
            area.innerHTML += `<div style="margin-top:8px;padding:8px;background:rgba(255,193,7,0.1);border:1px solid var(--yellow);border-radius:4px">
                <div style="color:var(--yellow);font-size:12px;font-weight:600;margin-bottom:4px">Warnings</div>
                ${data.warnings.map(w => `<div style="color:var(--yellow);font-size:11px;margin-top:2px">• ${escHtml(w)}</div>`).join('')}
            </div>`;
        }

        btn.textContent = 'Done';
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
        btn.textContent = 'Run';
        btn.disabled = false;
    }
}

// --- Deep Research ---

function renderUncertaintySources(sources, marketID) {
    const impactColors = { large: 'var(--red)', medium: 'var(--yellow)', small: 'var(--text-dim)' };

    let html = `
        <div class="uncertainty-section" style="margin-top:12px;border-top:1px solid var(--border);padding-top:12px">
            <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
                <h4 style="margin:0;color:var(--text)">Uncertainty Sources</h4>
                <button onclick="runResearch('${marketID}')" id="btn-research-${marketID}"
                        style="font-size:12px;padding:4px 12px">Research All</button>
            </div>`;

    html += sources.map((s, i) => `
        <div class="uncertainty-item" style="margin-bottom:8px;padding:8px;background:var(--bg);border-radius:4px">
            <div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">
                <span style="font-size:11px;padding:1px 6px;border-radius:3px;background:${impactColors[s.expected_impact] || 'var(--text-dim)'};color:var(--bg);font-weight:600">
                    ${s.expected_impact}
                </span>
                <span style="font-size:11px;padding:1px 6px;border-radius:3px;border:1px solid var(--border);color:var(--text-dim)">
                    ${s.domain}
                </span>
            </div>
            <div style="font-size:13px;color:var(--text);font-weight:500">${escHtml(s.question)}</div>
            <div style="font-size:12px;color:var(--text-dim);margin-top:2px">${escHtml(s.why_it_matters)}</div>
        </div>
    `).join('');

    html += `<div id="research-results-${marketID}"></div>`;
    html += `</div>`;
    return html;
}

async function runResearch(marketID) {
    const btn = document.getElementById(`btn-research-${marketID}`);
    const area = document.getElementById(`research-results-${marketID}`);

    btn.disabled = true;
    btn.textContent = 'Researching...';
    area.innerHTML = '<span class="loading"></span> Running Tavily searches for each uncertainty...';

    try {
        const resp = await fetch(`/api/research/${marketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        let html = `<div style="margin-top:8px;font-size:11px;color:var(--text-dim)">Cost: ${data.cost}</div>`;

        html += data.results.map((r, i) => {
            let resultHtml = `
                <div style="margin-top:10px;padding:8px;background:var(--bg);border-radius:4px;border-left:3px solid ${r.search_results && r.search_results.length > 0 ? 'var(--green)' : 'var(--yellow)'}">
                    <div style="font-size:13px;font-weight:500;color:var(--text)">${escHtml(r.question)}</div>`;

            if (r.answer) {
                resultHtml += `<div style="font-size:12px;color:var(--accent);margin-top:4px;padding:4px 8px;background:rgba(99,102,241,0.1);border-radius:3px">${escHtml(r.answer)}</div>`;
            }

            if (r.search_results && r.search_results.length > 0) {
                resultHtml += r.search_results.map(sr => `
                    <div style="margin-top:6px;font-size:12px">
                        <a href="${escHtml(sr.url)}" target="_blank" style="color:var(--accent)">${escHtml(sr.title)}</a>
                        ${sr.published_date ? `<span style="color:var(--text-dim);margin-left:6px">${escHtml(sr.published_date)}</span>` : ''}
                        <div style="color:var(--text-dim);margin-top:2px">${escHtml((sr.content || '').substring(0, 250))}</div>
                    </div>
                `).join('');
            } else {
                resultHtml += `<div style="font-size:12px;color:var(--yellow);margin-top:4px">No relevant sources found</div>`;
            }

            resultHtml += `</div>`;
            return resultHtml;
        }).join('');

        // Add Re-Audit button
        html += `
            <div style="margin-top:12px;text-align:center">
                <button onclick="runReaudit('${marketID}')" id="btn-reaudit-${marketID}"
                        style="padding:6px 20px;background:var(--accent);border:none;color:white;border-radius:4px;cursor:pointer">
                    Re-Audit with Research
                </button>
            </div>
            <div id="reaudit-results-${marketID}"></div>`;

        area.innerHTML = html;
        btn.textContent = 'Researched';
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
        btn.textContent = 'Research All';
        btn.disabled = false;
    }
}

async function runReaudit(marketID) {
    const btn = document.getElementById(`btn-reaudit-${marketID}`);
    const area = document.getElementById(`reaudit-results-${marketID}`);

    btn.disabled = true;
    btn.textContent = 'Running Re-Audit...';
    area.innerHTML = '<span class="loading"></span> Calling Claude Opus with research context — this takes 1-5 minutes...';

    try {
        const resp = await fetch(`/api/reaudit/${marketID}`, { method: 'POST' });
        const data = await resp.json();
        if (data.error) throw new Error(data.error);

        if (!data.matched || data.matched.length === 0) {
            area.innerHTML = '<span style="color:var(--yellow)">No matches — check market ID in response.</span>';
            btn.textContent = 'Re-Audit with Research';
            btn.disabled = false;
            return;
        }

        area.innerHTML = data.matched.map(m => {
            const isPositive = m.edge > 0;
            let html = `
                <div class="edge-result ${isPositive ? 'edge-positive' : 'edge-negative'}" style="margin-top:8px">
                    <div style="font-size:11px;color:var(--text-dim);margin-bottom:4px">UPDATED (post-research)</div>
                    <div class="edge-value" style="color:${isPositive ? 'var(--green)' : 'var(--red)'}">
                        ${isPositive ? '+' : ''}${m.edge_pct.toFixed(1)}% edge
                    </div>
                    <div class="edge-detail">
                        ${m.our_side} @ $${m.our_price.toFixed(3)} | ${probLine(m.true_prob, m.our_side)} | ${m.confidence}
                    </div>
                    <div class="edge-detail" style="margin-top:4px">${escHtml(m.reasoning || '')}</div>
                </div>`;

            if (m.uncertainty_sources && m.uncertainty_sources.length > 0) {
                html += renderUncertaintySources(m.uncertainty_sources, m.market_id + '-reaudit');
            }

            return html;
        }).join('');

        btn.textContent = 'Re-Audited';
    } catch (err) {
        area.innerHTML = `<span style="color:var(--red)">${escHtml(err.message)}</span>`;
        btn.textContent = 'Re-Audit with Research';
        btn.disabled = false;
    }
}

// --- Debug / Pipeline Audit ---

async function copyDebugPrompt(marketID) {
    const btn = document.querySelector(`.signal-item [onclick*="${marketID}"].btn-debug`) ||
                document.querySelector(`.btn-debug[onclick*="${marketID}"]`);

    try {
        if (btn) { btn.textContent = '⏳'; btn.disabled = true; }

        const resp = await fetch(`/api/debug/${marketID}`);
        if (!resp.ok) {
            const data = await resp.json().catch(() => ({}));
            throw new Error(data.error || `HTTP ${resp.status}`);
        }
        const data = await resp.json();

        await navigator.clipboard.writeText(data.prompt);

        if (btn) { btn.textContent = '✅'; }
        setTimeout(() => { if (btn) { btn.textContent = '🐛'; btn.disabled = false; } }, 2000);
    } catch (err) {
        alert('Debug copy failed: ' + err.message);
        if (btn) { btn.textContent = '🐛'; btn.disabled = false; }
    }
}

// --- Helpers ---

function updateSignalIndicator(marketID, field, value) {
    const sig = findSignal(marketID);
    if (sig) sig[field] = value;
    if (currentTab === 'portfolio') renderPortfolioList();
    else renderSignalList();
}

// probLine returns target side first, e.g. "NO 40.0% / YES 60.0%" when targeting NO
function probLine(trueProb, ourSide) {
    const targetProb = (trueProb * 100).toFixed(1);
    const altProb    = ((1 - trueProb) * 100).toFixed(1);
    const altSide    = ourSide === 'YES' ? 'NO' : 'YES';
    return `${ourSide} ${targetProb}% / ${altSide} ${altProb}%`;
}

function escHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ─── Monitor: Watch/Unwatch ───────────────────────────────────────────────────

async function toggleWatch(marketID) {
    const isWatched = watchlist.some(w => w.market_id === marketID);
    const method = isWatched ? 'DELETE' : 'POST';
    try {
        if (isWatched && liveTrackingID === marketID) stopTracking();
        await fetch(`/api/watch/${marketID}`, { method });
        await refreshWatchlist();
        // Update button text if detail is open
        const btn = document.getElementById('btn-watch');
        if (btn && selectedMarketID === marketID) {
            btn.textContent = watchlist.some(w => w.market_id === marketID) ? '★ Watching' : '☆ Watch';
        }
        renderSignalList();
        if (currentTab === 'portfolio') renderPortfolioList();
    } catch (e) {
        console.error('toggleWatch:', e);
    }
}

async function refreshWatchlist() {
    try {
        const resp = await fetch('/api/watch');
        watchlist = await resp.json() || [];
        document.getElementById('portfolio-count').textContent = watchlist.length;
    } catch (e) {
        console.error('refreshWatchlist:', e);
    }
}

// ─── Monitor: Portfolio Tab ───────────────────────────────────────────────────

function renderPortfolioList() {
    const list = document.getElementById('signal-list');
    if (!watchlist.length) {
        list.innerHTML = '<div style="padding:20px;color:var(--text-dim);text-align:center">No watched markets.<br>Click ☆ on a signal to watch it.</div>';
        return;
    }
    list.innerHTML = watchlist.map(wm => {
        const isSelected = wm.market_id === selectedMarketID;
        const price = wm.current_price ? `$${wm.current_price.toFixed(3)}` : '—';
        return `
            <div class="signal-item ${isSelected ? 'selected' : ''}" onclick="selectWatched('${wm.market_id}')">
                <div class="question">${escHtml(wm.question)}
                    <button class="watch-btn watching" onclick="event.stopPropagation();toggleWatch('${wm.market_id}')" title="Unwatch">★</button>
                    <button class="rule-btn" onclick="event.stopPropagation();openRuleModal('${wm.market_id}')" title="Configure rules">⚙</button>
                </div>
                <div class="meta">
                    <span>Price: ${price}</span>
                    <span>${wm.trade_count || 0} trades</span>
                    <span>${wm.last_poll_at ? 'polled ' + timeAgo(wm.last_poll_at) : 'pending'}</span>
                </div>
            </div>`;
    }).join('');
}

function selectWatched(marketID) {
    selectedMarketID = marketID;
    renderPortfolioList();
    // Show existing signal detail if available, otherwise show monitor panel
    const sig = findSignal(marketID);
    if (sig) {
        selectSignal(marketID);
    }
}

// ─── Monitor: Alert Feed ──────────────────────────────────────────────────────

function startAlertPolling() {
    if (alertPollTimer) return;
    pollAlerts();
    alertPollTimer = setInterval(pollAlerts, 5000); // poll every 5 sec
}

async function pollAlerts() {
    try {
        const resp = await fetch('/api/alerts');
        const data = await resp.json();
        if (!data) return;
        alertFeedItems = data;
        renderAlertFeed();
    } catch (e) { /* silent */ }
}

function renderAlertFeed() {
    const feed = document.getElementById('alert-feed');
    if (!alertFeedItems.length) {
        feed.innerHTML = '<div style="padding:8px;color:var(--text-dim);font-size:11px">No alerts yet</div>';
        return;
    }
    feed.innerHTML = alertFeedItems.slice(0, 30).map(a => {
        const sevClass = a.severity === 'alert' ? 'sev-alert' : a.severity === 'warning' ? 'sev-warning' : 'sev-info';
        const t = new Date(a.triggered_at).toLocaleTimeString();
        return `<div class="alert-item ${sevClass}" onclick="selectSignal('${a.market_id}')">
            <div class="alert-msg">${escHtml(a.message)}</div>
            <div class="alert-meta">${escHtml(a.rule_name)} · ${escHtml(a.market_q.substring(0, 40))} · ${t}</div>
        </div>`;
    }).join('');
}

function clearAlertFeed() {
    alertFeedItems = [];
    renderAlertFeed();
}

// ─── Monitor: Rule Config Modal ───────────────────────────────────────────────

async function openRuleModal(marketID) {
    ruleModalMarketID = marketID;
    const wm = watchlist.find(w => w.market_id === marketID);
    if (!wm) return;

    // Load rule catalogue if not cached
    if (!allRules.length) {
        const resp = await fetch('/api/rules');
        allRules = await resp.json() || [];
    }

    const enabledSet = new Set(wm.enabled_rules || []);
    const allEnabled = enabledSet.size === 0; // empty = all on

    document.getElementById('modal-title').textContent = 'Rules: ' + wm.question.substring(0, 50);

    const categories = [...new Set(allRules.map(r => r.category))];
    let html = '';
    for (const cat of categories) {
        html += `<div class="rule-category">${cat.toUpperCase()}</div>`;
        const catRules = allRules.filter(r => r.category === cat);
        for (const rule of catRules) {
            const on = allEnabled || enabledSet.has(rule.id);
            html += `<div class="rule-row">
                <label><input type="checkbox" class="rule-toggle" data-id="${rule.id}" ${on ? 'checked' : ''}> ${escHtml(rule.name)}</label>
            </div>`;
        }
    }
    document.getElementById('modal-body').innerHTML = html;
    document.getElementById('rule-modal').classList.remove('hidden');
}

function closeRuleModal() {
    document.getElementById('rule-modal').classList.add('hidden');
    ruleModalMarketID = null;
}

async function saveRules() {
    if (!ruleModalMarketID) return;
    const checkboxes = document.querySelectorAll('.rule-toggle');
    const enabled = [];
    checkboxes.forEach(cb => { if (cb.checked) enabled.push(cb.dataset.id); });

    await fetch(`/api/watch/${ruleModalMarketID}/rules`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled_rules: enabled, params: {} })
    });
    closeRuleModal();
    await refreshWatchlist();
}

// ─── Live Tracking ────────────────────────────────────────────────────────────

function toggleTrack(marketID) {
    if (liveTrackingID === marketID) {
        stopTracking();
        // Re-render detail to update button
        selectSignal(marketID);
        return;
    }
    if (liveTrackTimer) stopTracking();

    liveTrackingID = marketID;
    livePrevValues = {};

    document.getElementById('live-monitor').classList.remove('hidden');
    document.getElementById('live-stopped')?.remove();
    document.getElementById('live-metrics').innerHTML = 'Connecting... <span class="loading"></span>';

    pollLiveMetrics();
    liveTrackTimer = setInterval(pollLiveMetrics, 3000);

    // Re-render detail to update button state
    selectSignal(marketID);
}

function stopTracking() {
    if (liveTrackTimer) {
        clearInterval(liveTrackTimer);
        liveTrackTimer = null;
    }
    liveTrackingID = null;
    // Keep live-monitor visible with last values, just show "stopped" timestamp
    const monitor = document.getElementById('live-monitor');
    if (monitor && !monitor.classList.contains('hidden')) {
        const ts = new Date().toLocaleTimeString();
        let footer = document.getElementById('live-stopped');
        if (!footer) {
            footer = document.createElement('div');
            footer.id = 'live-stopped';
            footer.style.cssText = 'font-size:11px;color:var(--text-dim);padding:6px 8px;border-top:1px solid var(--border);margin-top:4px';
            monitor.appendChild(footer);
        }
        footer.textContent = `Tracking stopped · last update ${ts}`;
    }
}

async function pollLiveMetrics() {
    if (!liveTrackingID) return;
    try {
        const resp = await fetch(`/api/watch/${liveTrackingID}/live`);
        if (!resp.ok) return;
        const data = await resp.json();
        if (data.status === 'pending') {
            document.getElementById('live-metrics').innerHTML = 'Waiting for first poll... <span class="loading"></span>';
            return;
        }
        renderLiveMetrics(data);
    } catch (e) { /* silent */ }
}

const LIVE_FIELDS = [
    { key: 'mid_price',      label: 'Mid Price',   fmt: v => '$' + v.toFixed(4) },
    { key: 'best_bid',       label: 'Best Bid',    fmt: v => '$' + v.toFixed(4) },
    { key: 'best_ask',       label: 'Best Ask',    fmt: v => '$' + v.toFixed(4) },
    { key: 'spread_pct',     label: 'Spread',      fmt: v => v.toFixed(2) + '%' },
    { key: 'vwap',           label: 'VWAP',        fmt: v => '$' + v.toFixed(4) },
    { key: 'true_depth_usd', label: 'True Depth',  fmt: v => '$' + v.toFixed(0) },
    { key: 'full_depth_usd', label: 'Full Depth',  fmt: v => '$' + v.toFixed(0) },
    { key: 'dv_ratio',       label: 'D/V Ratio',   fmt: v => v.toFixed(4) },
];

function renderLiveMetrics(data) {
    const container = document.getElementById('live-metrics');

    const rows = LIVE_FIELDS.map(f => {
        const m = data[f.key];
        if (!m) return '';
        const val = m.value;
        const prev = livePrevValues[f.key];
        let flashClass = '';
        if (prev !== undefined && prev !== val) {
            flashClass = val > prev ? 'flash-green' : 'flash-red';
        }
        const passClass = m.passes ? 'pass' : 'fail';
        const passIcon = m.passes ? '✓' : '✗';
        const threshold = m.threshold ? `<span class="live-threshold">(${m.threshold})</span>` : '';
        livePrevValues[f.key] = val;

        return `<div class="live-row ${flashClass}" data-key="${f.key}">
            <span class="live-label">${f.label}</span>
            <span class="live-value">${f.fmt(val)} ${threshold}</span>
            <span class="live-pass ${passClass}">${passIcon}</span>
        </div>`;
    }).join('');

    container.innerHTML = rows;

    // Trigger fade: remove flash class after a frame so CSS transition kicks in
    setTimeout(() => {
        container.querySelectorAll('.flash-green, .flash-red').forEach(el => {
            el.classList.remove('flash-green', 'flash-red');
        });
    }, 50);
}

// ─── Comments ─────────────────────────────────────────────────────────────────

let commentsData = null; // { total, tree, flat } — cached for filter toggling

async function loadComments() {
    if (!selectedMarketID) return;
    const btn = document.getElementById('btn-comments');
    const area = document.getElementById('comments-results');
    btn.disabled = true;
    btn.textContent = 'Loading…';
    area.innerHTML = '<div style="color:var(--text-dim);padding:8px">Fetching comments…</div>';

    try {
        const resp = await fetch(`/api/comments/${selectedMarketID}`, { method: 'POST' });
        if (!resp.ok) {
            const msg = await resp.text();
            area.innerHTML = `<div style="color:var(--danger);padding:8px">${escHtml(msg)}</div>`;
            return;
        }
        commentsData = await resp.json();
        document.getElementById('comments-count').textContent = `(${commentsData.total})`;
        renderComments();
    } catch (e) {
        area.innerHTML = `<div style="color:var(--danger);padding:8px">Error: ${escHtml(e.message)}</div>`;
    } finally {
        btn.disabled = false;
        btn.textContent = 'Refresh';
    }
}

function renderComments() {
    const area = document.getElementById('comments-results');
    if (!commentsData) return;

    const holdersOnly = document.getElementById('comments-holders-only')?.checked;
    const nodes = commentsData.tree;

    if (!nodes || nodes.length === 0) {
        area.innerHTML = '<div style="color:var(--text-dim);padding:8px">No comments yet.</div>';
        return;
    }

    // Filter and count visible
    const visible = holdersOnly
        ? nodes.filter(n => n.market_position_usd > 0 || hasHolderInTree(n))
        : nodes;

    if (visible.length === 0) {
        area.innerHTML = '<div style="color:var(--text-dim);padding:8px">No comments from position holders.</div>';
        return;
    }

    area.innerHTML = visible.map(n => renderCommentNode(n, holdersOnly, 0)).join('');
}

// Returns true if this node or any reply has a market position.
function hasHolderInTree(node) {
    if (node.market_position_usd > 0) return true;
    return (node.replies || []).some(hasHolderInTree);
}

function renderCommentNode(node, holdersOnly, depth) {
    const indent = depth * 20;
    const hasPosition = node.market_position_usd > 0;

    // Skip non-holders in replies too when filter is on
    if (holdersOnly && depth > 0 && !hasHolderInTree(node)) return '';

    const name = node.profile?.name || node.profile?.pseudonym || shortenAddress(node.user_address || node.userAddress);
    const ago = timeAgo(node.createdAt || node.created_at);

    const positionBadge = hasPosition
        ? `<span style="font-size:11px;font-weight:600;padding:1px 6px;border-radius:3px;margin-left:6px;background:${node.position_side === 'YES' ? 'rgba(63,185,80,0.2)' : 'rgba(248,81,73,0.2)'};color:${node.position_side === 'YES' ? '#3fb950' : '#f85149'}">
               ${node.position_side} $${node.market_position_usd.toFixed(0)}
           </span>`
        : '';

    const reactions = node.reactionCount > 0
        ? `<span style="font-size:11px;color:var(--text-dim);margin-left:8px">❤ ${node.reactionCount}</span>`
        : '';

    const mediaHtml = (node.media || []).map(m =>
        m.mediaType === 'gif'
            ? `<img src="${escHtml(m.url)}" alt="${escHtml(m.altText || 'gif')}" style="max-width:200px;max-height:120px;display:block;margin-top:4px;border-radius:4px">`
            : ''
    ).join('');

    const replies = (node.replies || [])
        .filter(r => !holdersOnly || hasHolderInTree(r))
        .map(r => renderCommentNode(r, holdersOnly, depth + 1))
        .join('');

    return `
        <div style="margin-left:${indent}px;padding:8px 0;border-top:1px solid var(--border)">
            <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap;margin-bottom:4px">
                <span style="font-size:12px;font-weight:600;color:var(--text)">${escHtml(name)}</span>
                ${positionBadge}
                <span style="font-size:11px;color:var(--text-dim)">${ago}</span>
                ${reactions}
            </div>
            <div style="font-size:13px;color:var(--text-secondary);white-space:pre-wrap;word-break:break-word">${escHtml(node.body || '')}</div>
            ${mediaHtml}
        </div>
        ${replies}`;
}

function shortenAddress(addr) {
    if (!addr || addr.length < 10) return addr || 'unknown';
    return addr.slice(0, 6) + '…' + addr.slice(-4);
}

// ─── Utilities ────────────────────────────────────────────────────────────────

function timeAgo(isoStr) {
    const sec = Math.floor((Date.now() - new Date(isoStr).getTime()) / 1000);
    if (sec < 60) return `${sec}s ago`;
    if (sec < 3600) return `${Math.floor(sec/60)}m ago`;
    return `${Math.floor(sec/3600)}h ago`;
}

// ─── Initialisation ───────────────────────────────────────────────────────────

(async function init() {
    await refreshWatchlist();
    startAlertPolling();
})();
