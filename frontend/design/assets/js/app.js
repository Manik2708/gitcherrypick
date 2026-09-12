/* ==========================================================================
   GitCherryPick — hiring console.
   Router, screens and the discovery/shortlist behaviour.
   ========================================================================== */
(function () {
  'use strict';

  var G = window.GCP;
  var screenEl = document.getElementById('screen');
  var overlayHost = document.getElementById('overlay-host');
  var toastHost = document.getElementById('toast-host');

  /* ---------------------------------------------------------------- utils */
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function n1(v) { return v == null ? '—' : (Math.round(v * 10) / 10).toFixed(1); }
  function compact(v) {
    if (v == null) return '—';
    if (v >= 1000000) return (v / 1000000).toFixed(v >= 10000000 ? 0 : 1).replace(/\.0$/, '') + 'M';
    if (v >= 1000) return (v / 1000).toFixed(v >= 10000 ? 0 : 1).replace(/\.0$/, '') + 'k';
    return String(v);
  }
  function dateLabel(iso) {
    if (!iso) return '—';
    var d = new Date(iso + 'T00:00:00Z');
    return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });
  }
  function daysFromToday(iso) {
    return Math.round((new Date(iso + 'T00:00:00Z') - G.TODAY) / 86400000);
  }
  function relDays(iso) {
    var d = -daysFromToday(iso);
    if (d === 0) return 'today';
    if (d === 1) return 'yesterday';
    if (d < 30) return d + ' days ago';
    if (d < 365) return Math.round(d / 30.44) + ' months ago';
    return Math.round(d / 365) + ' years ago';
  }
  function icon(path, cls) {
    return '<svg class="ico ' + (cls || '') + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + path + '</svg>';
  }
  var ICO = {
    search: '<circle cx="11" cy="11" r="6.5"/><path d="m16 16 4.5 4.5"/>',
    check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
    clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>',
    alert: '<path d="M12 8v5M12 16.5v.01"/><circle cx="12" cy="12" r="8.5"/>',
    info: '<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5.5M12 7.8v.01"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    close: '<path d="m6 6 12 12M18 6 6 18"/>',
    chev: '<path d="m6 9 6 6 6-6"/>',
    back: '<path d="M11 6 5 12l6 6M5 12h14"/>',
    ext: '<path d="M14 5h5v5"/><path d="M19 5 11 13"/><path d="M18 14v4a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h4"/>',
    trash: '<path d="M5 7h14M10 7V5h4v2M7 7l1 12h8l1-12"/>',
    bookmark: '<path d="M7 4h10a1 1 0 0 1 1 1v15l-6-3.4L6 20V5a1 1 0 0 1 1-1Z"/>',
    lock: '<rect x="5" y="11" width="14" height="9" rx="1.6"/><path d="M8.5 11V8a3.5 3.5 0 0 1 7 0v3"/>',
    send: '<path d="M5 12 19 5l-4.5 14-3-6-6.5-1Z"/>',
    slash: '<circle cx="12" cy="12" r="8.5"/><path d="m6.5 6.5 11 11"/>'
  };

  function orgMark(mark, size, org, cls) {
    return '<img class="org-mark ' + (cls || '') + '" src="assets/img/repos/' + esc(mark) + '" width="' + size + '" height="' + size + '" alt="" loading="lazy" />';
  }
  function avatar(c, cls) {
    return '<span class="avatar ' + (cls || '') + '" data-tint="' + c.tint + '" aria-hidden="true">' + esc(c.initials) + '</span>';
  }

  /* --------------------------------------------------------------- toasts */
  function toast(msg, kind) {
    var t = document.createElement('div');
    t.className = 'toast';
    t.innerHTML = icon(kind === 'plain' ? ICO.info : ICO.check) + '<span>' + msg + '</span>';
    toastHost.appendChild(t);
    setTimeout(function () {
      t.classList.add('toast--leaving');
      setTimeout(function () { t.remove(); }, 240);
    }, 4200);
  }

  /* -------------------------------------------------------------- overlay */
  var lastFocus = null;
  function openModal(html, onMount) {
    lastFocus = document.activeElement;
    overlayHost.hidden = false;
    overlayHost.innerHTML = '<div class="scrim" data-close="1"></div>' +
      '<div class="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">' + html +
      '<button class="modal__close" data-close="1" aria-label="Close">' + icon(ICO.close) + '</button></div>';
    var modal = overlayHost.querySelector('.modal');
    var first = modal.querySelector('input, select, textarea, button:not(.modal__close)');
    (first || modal).focus();
    if (onMount) onMount(modal);
  }
  function closeModal() {
    overlayHost.hidden = true;
    overlayHost.innerHTML = '';
    if (lastFocus && lastFocus.isConnected && lastFocus.focus) lastFocus.focus();
    else screenEl.focus({ preventScroll: true });
  }
  overlayHost.addEventListener('click', function (e) {
    if (e.target.closest('[data-close]')) closeModal();
  });
  document.addEventListener('keydown', function (e) {
    if (overlayHost.hidden) return;
    if (e.key === 'Escape') { closeModal(); return; }
    if (e.key !== 'Tab') return;
    var f = overlayHost.querySelectorAll('a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])');
    if (!f.length) return;
    var first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  });

  /* ----------------------------------------------------- filters <-> hash */
  var DEFAULTS = {
    skills: [], min_skill_score: 0, min_overall_score: 0, min_generalist_score: 0,
    availability: [], include_inactive: false, evidence_within_months: null,
    q: '', page: 1, per_page: 20
  };
  function parseQuery(qs) {
    var f = JSON.parse(JSON.stringify(DEFAULTS));
    if (!qs) return f;
    qs.split('&').forEach(function (pair) {
      if (!pair) return;
      var i = pair.indexOf('='), k = decodeURIComponent(i < 0 ? pair : pair.slice(0, i));
      var v = i < 0 ? '' : decodeURIComponent(pair.slice(i + 1));
      if (k === 'skills' || k === 'availability') f[k] = v ? v.split(',') : [];
      else if (k === 'include_inactive') f[k] = v === 'true';
      else if (k === 'q') f.q = v;
      else if (k === 'evidence_within_months') f[k] = v === '' ? null : Number(v);
      else if (k in f) f[k] = Number(v);
    });
    return f;
  }
  function toQuery(f) {
    var out = [];
    Object.keys(DEFAULTS).forEach(function (k) {
      var v = f[k], d = DEFAULTS[k];
      if (Array.isArray(v)) { if (v.length) out.push(k + '=' + encodeURIComponent(v.join(','))); return; }
      if (v === d || v == null || v === '') return;
      out.push(k + '=' + encodeURIComponent(v));
    });
    return out.join('&');
  }
  var filterOverride = null;   /* set only when the URL cannot be rewritten */
  var urlWritable = true;

  function goSearch(f, replace) {
    var q = toQuery(f);
    var h = '#/search' + (q ? '?' + q : '');
    if (!replace) { location.hash = h; return; }
    /* Filtering re-renders the results; the control you are using keeps focus
       and its caret, so typing a name never gets interrupted. */
    var a = document.activeElement;
    var id = a && a.id ? a.id : null;
    var start = a && a.selectionStart != null ? a.selectionStart : null;
    if (urlWritable) {
      try { history.replaceState(null, '', h); filterOverride = null; }
      catch (err) { urlWritable = false; filterOverride = q; }
    } else { filterOverride = q; }
    render();
    if (id) {
      var next = document.getElementById(id);
      if (next) {
        next.focus({ preventScroll: true });
        if (start != null && next.setSelectionRange) {
          try { next.setSelectionRange(start, start); } catch (err) { /* type has no caret */ }
        }
      }
    }
  }

  /* -------------------------------------------------------- search engine
     Gates that are not parameters: never self, primary standing only for a
     skill filter, active rubric version, and never `not_looking`. */
  function activeRubric(c) { return c.rubricVersion === G.RUBRIC; }

  function rankedBy(f) {
    if (f.skills.length === 1) return 'skill:' + f.skills[0];
    if (f.min_generalist_score > 0 && f.skills.length !== 1) return 'generalist';
    return 'overall';
  }
  function orderValue(c, mode) {
    if (mode === 'generalist') return c.generalist;
    if (mode.indexOf('skill:') === 0) {
      var s = c.skills.filter(function (k) { return k.slug === mode.slice(6); })[0];
      return s ? s.score : -1;
    }
    return c.overall;
  }
  function globalRank(c, mode) {
    if (mode === 'generalist') return c.rankGeneralist;
    if (mode.indexOf('skill:') === 0) {
      var s = c.skills.filter(function (k) { return k.slug === mode.slice(6); })[0];
      return s ? s.rank : null;
    }
    return c.rankOverall;
  }

  function runSearch(f) {
    var mode = rankedBy(f);
    var q = f.q.trim().toLowerCase();

    var pool = G.CONTRIBUTORS.filter(function (c) {
      if (c.availability.status === 'not_looking') return false;   // gate, no toggle
      if (!activeRubric(c)) return false;                          // gate
      if (c.overall == null && !q) return false;                   // no primary skill, no ranking place
      return true;
    });

    var matched = pool.filter(function (c) {
      if (q) {
        if (c.name.toLowerCase().indexOf(q) < 0 && c.login.toLowerCase().indexOf(q) < 0) return false;
      }
      if (f.skills.length) {
        var hits = f.skills.filter(function (slug) {
          return c.skills.some(function (k) {
            return k.slug === slug && k.standing === 'primary' && k.score >= f.min_skill_score;
          });
        });
        if (hits.length !== f.skills.length) return false;         // AND
      } else if (f.min_skill_score > 0) {
        if (!c.skills.some(function (k) { return k.standing === 'primary' && k.score >= f.min_skill_score; })) return false;
      }
      if (f.min_overall_score > 0 && (c.overall == null || c.overall < f.min_overall_score)) return false;
      if (f.min_generalist_score > 0 && (c.generalist == null || c.generalist < f.min_generalist_score)) return false;
      if (f.availability.length && f.availability.indexOf(c.availability.status) < 0) return false;
      if (f.evidence_within_months != null && c.evidenceAgeMonths > f.evidence_within_months) return false;
      return true;
    });

    /* §1a — inactive hidden by default; §1b — a name lookup relaxes it. */
    var visible = matched, inactiveHidden = 0;
    if (!f.include_inactive && !q) {
      visible = matched.filter(function (c) { return c.availability.active; });
      inactiveHidden = matched.length - visible.length;
    }

    visible = visible.slice().sort(function (a, b) {
      var av = orderValue(a, mode), bv = orderValue(b, mode);
      if (av == null) return 1;
      if (bv == null) return -1;
      return bv - av || a.name.localeCompare(b.name);
    });

    var perPage = Math.min(50, f.per_page);
    var pages = Math.max(1, Math.ceil(visible.length / perPage));
    var page = Math.min(Math.max(1, f.page), pages);
    return {
      mode: mode, total: visible.length, inactiveHidden: inactiveHidden,
      pages: pages, page: page,
      rows: visible.slice((page - 1) * perPage, page * perPage)
    };
  }

  /* ------------------------------------------------------- shared markup */
  function availabilityPill(c) {
    var a = c.availability;
    var label = G.AVAILABILITY_LABEL[a.status];
    if (a.active) return '<span class="pill pill--primary"><span class="pill__dot"></span>' + esc(label) + '</span>';
    return '<span class="pill pill--stale">' + icon(ICO.clock, 'ico--xs') + esc(label) + ' · quiet ' + a.inactiveForDays + 'd</span>';
  }
  function skillPill(k) {
    return '<span class="skill-pill ' + (k.standing === 'primary' ? 'skill-pill--primary' : '') + '">' +
      esc(k.name) +
      '<span class="skill-pill__score mono">' + n1(k.score) + '</span></span>';
  }
  function rankLabel(mode) {
    if (mode === 'generalist') return 'generalist';
    if (mode.indexOf('skill:') === 0) return mode.slice(6);
    return 'overall';
  }

  /* =======================================================================
     SCREEN — discovery search
     ===================================================================== */
  function renderSearch(qs) {
    var f = parseQuery(qs);
    var res = runSearch(f);
    var mode = res.mode;

    var skillCounts = {};
    G.SKILLS.forEach(function (s) {
      skillCounts[s.slug] = G.CONTRIBUTORS.filter(function (c) {
        return c.availability.status !== 'not_looking' &&
          c.skills.some(function (k) { return k.slug === s.slug && k.standing === 'primary'; });
      }).length;
    });

    var filtersHtml =
      '<form class="filters" id="filters" novalidate>' +
        '<div class="filters__head">' +
          '<span class="panel-title">Filters</span>' +
          '<button type="button" class="btn btn--quiet btn--sm" data-act="reset">Reset</button>' +
        '</div>' +
        '<div class="filters__body">' +

          '<div class="fgroup">' +
            '<label class="field__label" for="q">Find someone by name</label>' +
            '<div class="search-input">' + icon(ICO.search, 'ico--sm') +
              '<input class="input" id="q" name="q" type="search" autocomplete="off" placeholder="Name or GitHub login" value="' + esc(f.q) + '" />' +
            '</div>' +
            '<p class="field__help">A name lookup also surfaces contributors who have gone quiet — you already know they exist.</p>' +
          '</div>' +

          '<div class="fgroup">' +
            '<div class="fgroup__title">Ranked skills <span class="muted" style="font-weight:400">· all must match</span></div>' +
            G.SKILLS.map(function (s) {
              return '<label class="checkline">' +
                '<input type="checkbox" name="skills" value="' + s.slug + '"' + (f.skills.indexOf(s.slug) >= 0 ? ' checked' : '') + ' />' +
                '<span class="checkline__text">' + esc(s.name) + '</span>' +
                '<span class="checkline__meta">' + skillCounts[s.slug] + '</span></label>';
            }).join('') +
            '<p class="field__help">Only <b>primary</b> standing — five distinct merged PRs — is searchable. A skill with four is visible on the scorecard and never in this list.</p>' +
          '</div>' +

          '<div class="fgroup">' +
            rangeField('min_skill_score', 'Minimum skill score', f.min_skill_score, 0, 100, 'Applies to every skill ticked above.') +
            rangeField('min_overall_score', 'Minimum overall score', f.min_overall_score, 0, 100, 'Depth: their best skill, plus headroom for breadth.') +
            rangeField('min_generalist_score', 'Minimum generalist score', f.min_generalist_score, 0, 220, 'Breadth, unbounded. Setting this orders the list by generalist.') +
          '</div>' +

          '<div class="fgroup">' +
            '<div class="fgroup__title">Availability</div>' +
            ['looking_for_job', 'looking_for_freelance', 'open_to_freelance'].map(function (st) {
              return '<label class="checkline">' +
                '<input type="checkbox" name="availability" value="' + st + '"' + (f.availability.indexOf(st) >= 0 ? ' checked' : '') + ' />' +
                '<span class="checkline__text">' + esc(G.AVAILABILITY_LABEL[st]) + '</span></label>';
            }).join('') +
            '<label class="switchline" style="margin-top:8px">' +
              '<span class="switchline__text"><span class="field__label">Include quiet profiles</span>' +
              '<span class="field__help">Availability lapses after 15 days. They stay ranked either way.</span></span>' +
              '<input type="checkbox" name="include_inactive"' + (f.include_inactive ? ' checked' : '') + ' />' +
              '<span class="switch" aria-hidden="true"></span>' +
            '</label>' +
          '</div>' +

          '<div class="fgroup">' +
            '<label class="field__label" for="evidence">Evidence recency</label>' +
            '<select class="select" id="evidence" name="evidence_within_months">' +
              [['', 'Any age — no filter'], ['6', 'Newest PR within 6 months'], ['12', 'Newest PR within 12 months'], ['24', 'Newest PR within 24 months']]
                .map(function (o) {
                  var sel = String(f.evidence_within_months == null ? '' : f.evidence_within_months) === o[0];
                  return '<option value="' + o[0] + '"' + (sel ? ' selected' : '') + '>' + o[1] + '</option>';
                }).join('') +
            '</select>' +
            '<p class="field__help">How old their <b>code</b> is — unrelated to how recently they said they were open.</p>' +
          '</div>' +

          '<div class="fgroup">' +
            '<label class="field__label" for="per_page">Results per page</label>' +
            '<select class="select" id="per_page" name="per_page">' +
              [10, 20, 50].map(function (v) {
                return '<option value="' + v + '"' + (f.per_page === v ? ' selected' : '') + '>' + v + ' per page</option>';
              }).join('') +
            '</select>' +
          '</div>' +
        '</div>' +
        '<div class="filters__foot">' +
          '<button type="button" class="btn btn--ghost btn--sm od-fill" data-act="save-search">' + icon(ICO.bookmark, 'ico--sm') + 'Save this search</button>' +
        '</div>' +
      '</form>';

    var banner = '';
    if (res.inactiveHidden > 0) {
      banner = '<div class="banner">' + icon(ICO.info) +
        '<div class="banner__body"><b>' + res.inactiveHidden + ' ' +
        (res.inactiveHidden === 1 ? 'contributor is' : 'contributors are') + ' hidden because their availability lapsed.</b> ' +
        'They keep their place in the global ranking, so the numbers below skip theirs. ' +
        '<button type="button" class="btn btn--quiet btn--sm" data-act="show-inactive" style="padding:0 4px">Show them</button></div></div>';
    }
    if (f.q.trim()) {
      banner += '<div class="banner banner--info">' + icon(ICO.search) +
        '<div class="banner__body">Name lookup: quiet profiles are included, because you are looking for somebody you already know exists.</div></div>';
    }

    var rowsHtml = res.rows.length
      ? '<ul class="rows">' + res.rows.map(function (c) { return searchRow(c, mode); }).join('') + '</ul>'
      : emptyState(f);

    var pager = res.pages > 1 ? pagerHtml(res) : '';

    screenEl.innerHTML =
      '<div class="page">' +
        '<div class="page-head">' +
          '<div>' +
            '<p class="eyebrow">Discovery</p>' +
            '<h1 class="page-head__title">Search the pool</h1>' +
            '<p class="page-head__lede">Every score here rests on five merged pull requests a model has read. Filter the ranking, then read the evidence before you commit a round to anyone.</p>' +
          '</div>' +
          '<div class="page-head__actions">' +
            '<a class="btn btn--ghost" href="#/leaderboard">View the full board</a>' +
          '</div>' +
        '</div>' +
        '<div class="discovery">' +
          filtersHtml +
          '<section aria-label="Results">' +
            '<div class="results-head">' +
              '<p class="results-count"><strong>' + res.total + '</strong> ' + (res.total === 1 ? 'contributor' : 'contributors') +
              (res.inactiveHidden ? ' · <span class="muted">' + res.inactiveHidden + ' hidden</span>' : '') + '</p>' +
              '<span class="ranked-by">Ranked by <code>' + esc(rankLabel(mode)) + '</code></span>' +
            '</div>' +
            banner + rowsHtml + pager +
          '</section>' +
        '</div>' +
      '</div>';

    wireFilters(f);
  }

  function rangeField(name, label, value, min, max, help) {
    return '<div class="field">' +
      '<div class="range-head"><label class="field__label" for="' + name + '">' + label + '</label>' +
      '<span class="range-value" data-out="' + name + '">' + (value > 0 ? value : 'any') + '</span></div>' +
      '<input class="range" type="range" id="' + name + '" name="' + name + '" min="' + min + '" max="' + max + '" step="1" value="' + value + '" />' +
      '<p class="field__help">' + help + '</p></div>';
  }

  function searchRow(c, mode) {
    var rank = globalRank(c, mode);
    var inactive = !c.availability.active;
    var leadSkill = mode.indexOf('skill:') === 0
      ? c.skills.filter(function (k) { return k.slug === mode.slice(6); })[0]
      : null;

    return '<li class="row ' + (inactive ? 'row--inactive' : '') + (rank && rank <= 3 ? ' rank--top' : '') + '">' +
      '<div class="rank ' + (rank && rank <= 3 ? 'rank--top' : '') + '">' +
        '<span class="rank__n">' + (rank == null ? '—' : '#' + rank) + '</span>' +
        '<span class="rank__label">' + esc(rankLabel(mode)) + '</span>' +
      '</div>' +
      avatar(c) +
      '<div class="row__mid">' +
        '<div class="row__ident">' +
          '<a class="row__name" href="#/contributors/' + esc(c.id) + '">' + esc(c.name) + '</a>' +
          '<span class="row__login mono">@' + esc(c.login) + '</span>' +
        '</div>' +
        '<p class="row__headline od-clamp-2">' + esc(c.headline) + '</p>' +
        '<div class="row__skills">' +
          c.skills.slice(0, 4).map(skillPill).join('') +
        '</div>' +
        '<div class="row__meta">' +
          '<span>' + esc(c.location) + ' · ' + esc(c.timezone) + '</span>' +
          '<span>Newest evidence ' + relDays(c.newestEvidence) + '</span>' +
          (inactive
            ? '<span>Confirmed ' + dateLabel(c.availability.lastConfirmedAt) + ' · quiet for <b class="mono">' + c.availability.inactiveForDays + '</b> days</span>'
            : '') +
        '</div>' +
      '</div>' +
      '<div class="row__right">' +
        '<div class="row__scores">' +
          (leadSkill
            ? '<span class="stat"><span class="stat__n">' + n1(leadSkill.score) + '</span><span class="stat__label">' + esc(leadSkill.name) + '</span></span>'
            : '') +
          '<span class="stat"><span class="stat__n">' + n1(c.overall) + '</span><span class="stat__label">Overall</span></span>' +
          '<span class="stat"><span class="stat__n stat__n--muted">' + n1(c.generalist) + '</span><span class="stat__label">Generalist</span></span>' +
        '</div>' +
        availabilityPill(c) +
        '<div class="row__actions">' +
          '<a class="btn btn--ghost btn--sm" href="#/contributors/' + esc(c.id) + '">Scorecard</a>' +
          '<button class="btn btn--primary btn--sm" data-act="stage" data-user="' + esc(c.id) + '">' + icon(ICO.plus, 'ico--sm') + 'Shortlist</button>' +
        '</div>' +
      '</div>' +
    '</li>';
  }

  function emptyState(f) {
    var why = [];
    if (f.skills.length > 1) why.push('all ' + f.skills.length + ' skills must be <b>primary</b> for the same person');
    if (f.min_skill_score > 0) why.push('skill score at or above ' + f.min_skill_score);
    if (f.min_overall_score > 0) why.push('overall at or above ' + f.min_overall_score);
    if (f.min_generalist_score > 0) why.push('generalist at or above ' + f.min_generalist_score);
    if (f.evidence_within_months != null) why.push('newest merged PR within ' + f.evidence_within_months + ' months');
    return '<div class="empty">' +
      '<span class="empty__icon">' + icon(ICO.slash) + '</span>' +
      '<p class="empty__title">Nobody clears every filter</p>' +
      '<p class="empty__body">' + (why.length
        ? 'You are asking for ' + why.join(', ') + '. Skills combine with AND, so each extra one narrows hard.'
        : 'No contributor in the active rubric version matches.') + '</p>' +
      '<div class="empty__actions">' +
        '<button class="btn btn--ghost btn--sm" data-act="relax">Loosen the score thresholds</button>' +
        '<button class="btn btn--ghost btn--sm" data-act="reset">Reset all filters</button>' +
      '</div></div>';
  }

  function pagerHtml(res) {
    var out = '<nav class="pager" aria-label="Pagination"><p class="small muted">Page ' + res.page + ' of ' + res.pages + '</p><div class="pager__pages">';
    for (var i = 1; i <= res.pages; i++) {
      out += '<button type="button" data-act="page" data-page="' + i + '"' + (i === res.page ? ' aria-current="page"' : '') + '>' + i + '</button>';
    }
    return out + '</div></nav>';
  }

  function readFilters(form, base) {
    var f = JSON.parse(JSON.stringify(base));
    f.q = form.querySelector('#q').value;
    f.skills = Array.prototype.slice.call(form.querySelectorAll('input[name=skills]:checked')).map(function (i) { return i.value; });
    f.availability = Array.prototype.slice.call(form.querySelectorAll('input[name=availability]:checked')).map(function (i) { return i.value; });
    f.include_inactive = form.querySelector('input[name=include_inactive]').checked;
    f.min_skill_score = Number(form.querySelector('#min_skill_score').value);
    f.min_overall_score = Number(form.querySelector('#min_overall_score').value);
    f.min_generalist_score = Number(form.querySelector('#min_generalist_score').value);
    var ev = form.querySelector('#evidence').value;
    f.evidence_within_months = ev === '' ? null : Number(ev);
    f.per_page = Number(form.querySelector('#per_page').value);
    f.page = 1;
    return f;
  }

  function wireFilters(f) {
    var form = document.getElementById('filters');
    if (!form) return;
    var debounce;

    /* A slider updates its readout as you drag and re-queries when you let go,
       so the results never redraw out from under the thumb. */
    form.addEventListener('input', function (e) {
      if (e.target.type === 'range') {
        var out = form.querySelector('[data-out="' + e.target.name + '"]');
        if (out) out.textContent = Number(e.target.value) > 0 ? e.target.value : 'any';
        return;
      }
      if (e.target.type !== 'search' && e.target.type !== 'text') return;
      clearTimeout(debounce);
      debounce = setTimeout(function () { goSearch(readFilters(form, f), true); }, 280);
    });
    form.addEventListener('change', function (e) {
      if (e.target.type === 'search' || e.target.type === 'text') return;
      goSearch(readFilters(form, f), true);
    });
    form.addEventListener('submit', function (e) { e.preventDefault(); });

  }

  /* =======================================================================
     SCREEN — scorecard
     ===================================================================== */
  function renderScorecard(id) {
    var c = G.BY_ID[id];
    if (!c) { renderNotFound(); return; }
    if (c.availability.status === 'not_looking') { renderOptedOut(); return; }

    var primaries = c.skills.filter(function (k) { return k.standing === 'primary'; });
    var secondaries = c.skills.filter(function (k) { return k.standing === 'secondary'; });
    var inactive = !c.availability.active;

    var inShortlists = G.SHORTLISTS.filter(function (s) {
      return s.entries.some(function (e) { return e.userId === c.id; });
    });

    var staleNotice = inactive
      ? '<div class="notice">' +
          '<p class="notice__title">' + icon(ICO.clock, 'ico--sm') + 'Availability lapsed ' + c.availability.inactiveForDays + ' days ago</p>' +
          '<p class="notice__body">Last confirmed ' + dateLabel(c.availability.lastConfirmedAt) + '. Lapsing means they have not clicked a button recently — not that they said no. ' +
          'They keep rank <b class="mono">#' + c.rankOverall + '</b> overall, and a contact request still reaches them; accepting it refreshes their window.</p>' +
        '</div>'
      : '';

    var noScore = c.overall == null
      ? '<div class="notice"><p class="notice__title">' + icon(ICO.alert, 'ico--sm') + 'No overall score yet</p>' +
        '<p class="notice__body">An overall score needs at least one <b>primary</b> skill — five distinct merged PRs evidencing the same thing. ' +
        'Everything below is scored and real; there is simply no headline number and no place in any ranking until a fifth PR lands.</p></div>'
      : '';

    screenEl.innerHTML =
      '<div class="page">' +
        '<nav class="crumb" aria-label="Breadcrumb">' +
          '<a href="#/search">' + icon(ICO.back, 'ico--xs') + ' Search</a><span>/</span><span>' + esc(c.name) + '</span>' +
        '</nav>' +

        '<header class="sc-head">' +
          avatar(c, 'avatar--lg') +
          '<div class="sc-ident">' +
            '<h1 class="sc-name">' + esc(c.name) + '</h1>' +
            '<p class="sc-login mono">@' + esc(c.login) + '</p>' +
            '<p class="sc-bio">' + esc(c.headline) + '</p>' +
            '<div class="sc-facts">' +
              '<span>' + esc(c.location) + ' · ' + esc(c.timezone) + '</span>' +
              '<span><b class="mono">' + compact(c.mergedContributions) + '</b> merged contributions</span>' +
              '<span><b class="mono">' + c.reposTouched + '</b> repositories</span>' +
              '<span><b class="mono">' + compact(c.followers) + '</b> followers</span>' +
              '<span>Joined ' + dateLabel(c.joined) + '</span>' +
            '</div>' +
            '<div class="od-cluster" style="--od-gap:8px">' + availabilityPill(c) +
              '<span class="pill">Rubric <span class="mono">' + esc(c.rubricVersion) + '</span></span>' +
              (inShortlists.length ? '<span class="pill pill--cherry">' + icon(ICO.bookmark, 'ico--xs') + 'On ' + inShortlists.length + ' of your shortlists</span>' : '') +
            '</div>' +
          '</div>' +
          '<div class="sc-actions">' +
            '<button class="btn btn--primary" data-act="stage" data-user="' + esc(c.id) + '">' + icon(ICO.plus, 'ico--sm') + 'Add to a shortlist</button>' +
            '<a class="btn btn--ghost" href="https://github.com/' + esc(c.login) + '" target="_blank" rel="noopener">' + icon(ICO.ext, 'ico--sm') + 'GitHub profile</a>' +
          '</div>' +
        '</header>' +

        (noScore || staleNotice ? '<div style="margin-top:var(--s3)">' + noScore + staleNotice + '</div>' : '') +

        '<div class="headline-scores" style="margin-top:var(--s3)">' +
          '<div class="hscore hscore--lead">' +
            '<span class="hscore__label">Overall · depth</span>' +
            '<span class="hscore__n">' + n1(c.overall) + '</span>' +
            (c.rankOverall ? '<span class="hscore__rank">Rank #' + c.rankOverall + ' of ' + rankedPopulation() + '</span>' : '<span class="hscore__rank">Unranked</span>') +
            '<p class="hscore__note">Their best skill is the floor; every other skill fills half the remaining headroom, each worth half the one before.</p>' +
          '</div>' +
          '<div class="hscore">' +
            '<span class="hscore__label">Generalist · breadth</span>' +
            '<span class="hscore__n">' + n1(c.generalist) + '</span>' +
            (c.rankGeneralist ? '<span class="hscore__rank">Rank #' + c.rankGeneralist + ' of ' + rankedPopulation() + '</span>' : '<span class="hscore__rank">Unranked</span>') +
            '<p class="hscore__note">Unbounded, and it can order people differently from overall. Both numbers are true.</p>' +
          '</div>' +
        '</div>' +

        (c.overall != null ? overallMathPanel(c) : '') +

        '<section class="section">' +
          '<div class="section__head">' +
            '<h2 class="section__title">Skill standing</h2>' +
            '<p class="section__note">The score says what the work was worth. The PR count says whether it is rankable. They are different questions.</p>' +
          '</div>' +
          '<div class="standing">' +
            primaries.map(function (k) { return standingRow(c, k); }).join('') +
            secondaries.map(function (k) { return standingRow(c, k); }).join('') +
          '</div>' +
          (secondaries.length
            ? '<p class="small muted" style="margin-top:12px;max-width:70ch">' +
              esc(secondaries[0].name) + ' scores ' + n1(secondaries[0].score) + ' on ' + secondaries[0].prCount +
              ' distinct ' + (secondaries[0].prCount === 1 ? 'PR' : 'PRs') + ' and is <b>not</b> searchable — five is what makes a skill comparable. ' +
              'Nothing promotes it except a fifth distinct PR, and no score is capped to keep the ordering tidy.</p>'
            : '') +
        '</section>' +

        (c.projects.length ? projectsSection(c) : '') +

        '<section class="section">' +
          '<div class="section__head">' +
            '<h2 class="section__title">The evidence</h2>' +
            '<p class="section__note">Every pull request below was read once, against every skill it was claimed for. Open one to see what the model judged and what it counted.</p>' +
          '</div>' +
          c.evidence.map(function (e, i) { return evidenceCard(e, i); }).join('') +
        '</section>' +
      '</div>';
  }

  function rankedPopulation() {
    return G.CONTRIBUTORS.filter(function (c) { return c.rankOverall != null; }).length;
  }

  function overallMathPanel(c) {
    var ladder = c.valueLadder;
    return '<div class="card card--pad" style="margin-top:var(--s2)">' +
      '<p class="eyebrow">How the overall score was reached</p>' +
      '<div class="formula" style="margin-top:10px">' +
        ladder.map(function (x, i) {
          var k = c.skills.filter(function (s) { return s.slug === x.slug; })[0];
          var note = k.slug === 'pr-review' ? ' · pr-review ×1.2' : (k.standing === 'secondary' ? ' · secondary ×0.5' : '');
          return 'v' + (i + 1) + ' = <b>' + n1(x.value) + '</b>  ' + esc(k.name) + ' (' + n1(k.score) + note + ')';
        }).join('<br />') +
        '<br /><br />B = <b>' + c.breadth.toFixed(2) + '</b>   Overall = v1 + (100 − v1) × 0.5 × B = <b>' + n1(c.overall) + '</b>' +
      '</div>' +
      '<p class="small muted" style="margin-top:10px;max-width:76ch">A second skill always helps and never hurts: it can only spend the headroom above the first one. ' +
      'That is why a strong specialist is not punished for also being broad.</p>' +
    '</div>';
  }

  function standingRow(c, k) {
    var isPrimary = k.standing === 'primary';
    return '<div class="standing__item ' + (isPrimary ? '' : 'standing__item--secondary') + '">' +
      '<div class="od-field">' +
        '<span class="standing__name">' + esc(k.name) + '</span>' +
        '<span class="standing__meta">' +
          '<b class="mono">' + k.prCount + '</b> distinct ' + (k.prCount === 1 ? 'PR' : 'PRs') + ' · ' +
          (isPrimary
            ? 'PR component <b class="mono">' + n1(k.prComponent) + '</b> (mean of the best five)'
            : 'PR component <b class="mono">' + n1(k.prComponent) + '</b> (mean × ' + k.prCount + '/5)') +
          (k.projectComponent > 0 ? ' · project reach <b class="mono">' + k.projectComponent.toFixed(2) + '</b>' : '') +
        '</span>' +
      '</div>' +
      '<div class="standing__right">' +
        (isPrimary
          ? '<span class="pill pill--primary">Primary · rank #' + (k.rank || '—') + '</span>'
          : '<span class="pill">Secondary · unranked</span>') +
        '<span class="meter" role="img" aria-label="Score ' + n1(k.score) + ' out of 100"><span class="meter__fill" style="width:' + Math.min(100, k.score) + '%"></span></span>' +
        '<span class="stat"><span class="stat__n">' + n1(k.score) + '</span><span class="stat__label">Skill score</span></span>' +
      '</div>' +
    '</div>';
  }

  function projectsSection(c) {
    return '<section class="section">' +
      '<div class="section__head">' +
        '<h2 class="section__title">Projects</h2>' +
        '<p class="section__note">Scored arithmetically only — stars, forks, contributors, dependents, downloads. The model is never asked to judge a project.</p>' +
      '</div>' +
      '<div class="card">' +
        c.projects.map(function (p) {
          return '<div class="entry">' +
            orgMark(p.mark, p.markSize, p.org, 'org-mark--lg') +
            '<div class="od-field">' +
              '<span class="entry__name mono">' + esc(p.repo) + '</span>' +
              '<span class="entry__meta">' +
                compact(p.facts.stars) + ' stars · ' + compact(p.facts.forks) + ' forks · ' +
                compact(p.facts.contributors) + ' contributors · ' + compact(p.facts.dependents) + ' dependents' +
              '</span>' +
            '</div>' +
            (p.maintainer ? '<span class="pill pill--violet">Maintainer · reach ×1.25</span>' : '<span class="pill">Contributor</span>') +
            '<span class="stat"><span class="stat__n">' + p.reach.toFixed(2) + '</span><span class="stat__label">Reach R</span></span>' +
          '</div>';
        }).join('') +
      '</div>' +
    '</section>';
  }

  function evidenceCard(e, i) {
    var dims = [
      ['Contribution substance', e.dims.substance, 0.30],
      ['Complexity', e.dims.complexity, 0.25],
      ['Conversation quality', e.dims.conversation_quality, 0.20],
      ['Craft — tests, docs, clarity', e.dims.craft, 0.15],
      ['Skill specificity', e.dims.skill_specificity, 0.10]
    ];
    var skillName = G.SKILL_BY_SLUG[e.skill].name;
    var formula = e.judgedOnly
      ? '100 × [ 0.80 × (Q/100) + 0.20 × R ] = <b>' + n1(e.score) + '</b>' +
        '<br /><span class="muted">judged_only — no PR-level arithmetic: the counts describe the author’s work, not the reviewer’s.</span>'
      : '100 × [ 0.70 × (Q/100) + 0.20 × R + 0.10 × E ]<br />' +
        '100 × [ 0.70 × ' + (e.q / 100).toFixed(3) + ' + 0.20 × ' + e.reach.toFixed(3) + ' + 0.10 × ' + e.engagement.toFixed(3) + ' ] = <b>' + n1(e.score) + '</b>';

    return '<article class="ev">' +
      '<button class="ev__head" type="button" aria-expanded="false" data-act="toggle-ev" data-ev="' + i + '">' +
        orgMark(e.mark, e.markSize, e.org, 'org-mark--lg') +
        '<span class="od-field">' +
          '<span class="ev__title">' + esc(e.title) + '</span>' +
          '<span class="ev__repo">' + esc(e.repo) + ' #' + e.number + ' · merged ' + relDays(e.mergedAt) +
          (e.role === 'reviewer' ? ' · as reviewer' : '') + '</span>' +
        '</span>' +
        '<span class="ev__right">' +
          '<span class="pill ' + (e.judgedOnly ? 'pill--violet' : '') + '">' + esc(skillName) + '</span>' +
          '<span class="ev__score">' + n1(e.score) + '</span>' +
          '<span class="ev__chev">' + icon(ICO.chev, 'ico--sm') + '</span>' +
        '</span>' +
      '</button>' +
      '<div class="ev__body" id="ev-' + i + '" hidden>' +
        '<p class="rationale">' + esc(e.rationale) + '</p>' +
        '<div>' +
          '<p class="eyebrow" style="margin-bottom:8px">Judged quality — Q = ' + n1(e.q) + '</p>' +
          '<div class="dims">' + dims.map(function (d) {
            return '<div class="dim">' +
              '<span class="dim__label">' + d[0] + ' <span class="dim__weight">×' + d[2].toFixed(2) + '</span></span>' +
              '<span class="dim__bar"><span class="dim__fill" style="width:' + d[1] + '%"></span></span>' +
              '<span class="dim__val">' + d[1] + '</span></div>';
          }).join('') + '</div>' +
        '</div>' +
        '<div>' +
          '<p class="eyebrow" style="margin-bottom:8px">Counted, not judged</p>' +
          '<div class="facts">' +
            '<span class="fact">reach R ' + e.reach.toFixed(3) + (e.maintainer ? ' (maintainer ×1.25)' : '') + '</span>' +
            (e.judgedOnly ? '' : '<span class="fact">engagement E ' + e.engagement.toFixed(3) + '</span>') +
            '<span class="fact">' + compact(e.repoFacts.stars) + ' stars</span>' +
            '<span class="fact">' + compact(e.repoFacts.dependents) + ' dependents</span>' +
            '<span class="fact">' + e.facts.reviews + ' reviews</span>' +
            '<span class="fact">' + e.facts.review_comments + ' review comments</span>' +
            '<span class="fact">' + e.facts.participants + ' participants</span>' +
            '<span class="fact">+' + e.facts.additions + ' −' + e.facts.deletions + ' across ' + e.facts.files + ' files</span>' +
          '</div>' +
          '<p class="small muted" style="margin-top:8px">Diff size is context passed to the model, never a scored term. A 4,000-line generated-file change is not four thousand lines of engineering.</p>' +
        '</div>' +
        '<div class="formula">' + formula + '</div>' +
        '<div><a class="btn btn--ghost btn--sm" href="https://github.com/' + esc(e.repo) + '/pull/' + e.number + '" target="_blank" rel="noopener">' +
          icon(ICO.ext, 'ico--sm') + 'Open the pull request</a></div>' +
      '</div>' +
    '</article>';
  }

  function renderNotFound() {
    screenEl.innerHTML = '<div class="page page--narrow"><div class="empty">' +
      '<span class="empty__icon">' + icon(ICO.slash) + '</span>' +
      '<p class="empty__title">No such contributor</p>' +
      '<p class="empty__body">That scorecard does not exist, or it belongs to somebody outside the active rubric version.</p>' +
      '<div class="empty__actions"><a class="btn btn--primary btn--sm" href="#/search">Back to search</a></div></div></div>';
  }
  function renderOptedOut() {
    screenEl.innerHTML = '<div class="page page--narrow"><div class="empty">' +
      '<span class="empty__icon">' + icon(ICO.lock) + '</span>' +
      '<p class="empty__title">This contributor has opted out</p>' +
      '<p class="empty__body">They set their availability to <b>not looking</b>. That is an explicit choice rather than a lapsed one, so no filter, toggle or board reveals them.</p>' +
      '<div class="empty__actions"><a class="btn btn--primary btn--sm" href="#/search">Back to search</a></div></div></div>';
  }

  /* =======================================================================
     SCREEN — leaderboard
     ===================================================================== */
  var boardState = { kind: 'overall', skill: 'go' };
  function renderLeaderboard() {
    var ranked = G.CONTRIBUTORS.filter(function (c) { return c.rankOverall != null; });
    var rows, valueLabel;

    if (boardState.kind === 'skill') {
      rows = ranked.filter(function (c) {
        return c.skills.some(function (k) { return k.slug === boardState.skill && k.standing === 'primary'; });
      }).map(function (c) {
        var k = c.skills.filter(function (s) { return s.slug === boardState.skill; })[0];
        return { c: c, rank: k.rank, value: k.score };
      }).sort(function (a, b) { return a.rank - b.rank; });
      valueLabel = G.SKILL_BY_SLUG[boardState.skill].name + ' score';
    } else if (boardState.kind === 'generalist') {
      rows = ranked.map(function (c) { return { c: c, rank: c.rankGeneralist, value: c.generalist }; })
        .sort(function (a, b) { return a.rank - b.rank; });
      valueLabel = 'Generalist';
    } else {
      rows = ranked.map(function (c) { return { c: c, rank: c.rankOverall, value: c.overall }; })
        .sort(function (a, b) { return a.rank - b.rank; });
      valueLabel = 'Overall';
    }

    screenEl.innerHTML =
      '<div class="page">' +
        '<div class="page-head">' +
          '<div>' +
            '<p class="eyebrow">Ranking</p>' +
            '<h1 class="page-head__title">Leaderboard</h1>' +
            '<p class="page-head__lede">The definitive ranking. Search is the actionable subset of it — so when a rank is missing from your results, this is where you find out who fills it.</p>' +
          '</div>' +
        '</div>' +

        '<div class="od-cluster" style="--od-gap:12px;margin-bottom:var(--s2)">' +
          '<div class="segmented" role="group" aria-label="Board">' +
            ['overall', 'generalist', 'skill'].map(function (k) {
              return '<button type="button" data-act="board" data-kind="' + k + '" aria-pressed="' + (boardState.kind === k) + '">' +
                (k === 'skill' ? 'By skill' : k[0].toUpperCase() + k.slice(1)) + '</button>';
            }).join('') +
          '</div>' +
          (boardState.kind === 'skill'
            ? '<select class="select" style="width:auto" data-act="board-skill" aria-label="Skill">' +
              G.SKILLS.map(function (s) {
                return '<option value="' + s.slug + '"' + (boardState.skill === s.slug ? ' selected' : '') + '>' + esc(s.name) + '</option>';
              }).join('') + '</select>'
            : '') +
          '<span class="pill">' + icon(ICO.info, 'ico--xs') + 'Everyone is listed, quiet profiles included</span>' +
        '</div>' +

        (boardState.kind === 'skill'
          ? '<p class="small muted" style="margin-bottom:12px;max-width:76ch">Primary standing only. A contributor holding this skill on four PRs is scored and visible on their scorecard, however high — and never on this board.</p>'
          : '') +

        '<div class="board">' +
          '<table>' +
            '<caption class="vh">' + valueLabel + ' leaderboard</caption>' +
            '<thead><tr>' +
              '<th class="rankcell">Rank</th><th>Contributor</th>' +
              '<th class="hide-sm">Standing</th><th class="hide-sm">Availability</th>' +
              '<th class="num">' + valueLabel + '</th>' +
            '</tr></thead>' +
            '<tbody>' +
              rows.map(function (r) {
                var c = r.c;
                return '<tr class="' + (r.rank <= 3 ? 'is-top' : '') + '">' +
                  '<td class="rankcell">#' + r.rank + '</td>' +
                  '<td><span class="who">' + avatar(c, 'avatar--sm') +
                    '<span class="od-field od-fill"><a href="#/contributors/' + esc(c.id) + '">' + esc(c.name) + '</a>' +
                    '<span class="row__login mono od-truncate">@' + esc(c.login) + '</span></span></span></td>' +
                  '<td class="hide-sm"><span class="od-cluster" style="--od-gap:4px">' +
                    c.skills.filter(function (k) { return k.standing === 'primary'; }).slice(0, 3)
                      .map(function (k) { return '<span class="pill pill--primary">' + esc(k.name) + '</span>'; }).join('') +
                  '</span></td>' +
                  '<td class="hide-sm">' + availabilityPill(c) + '</td>' +
                  '<td class="num"><b>' + n1(r.value) + '</b></td>' +
                '</tr>';
              }).join('') +
            '</tbody>' +
          '</table>' +
        '</div>' +
        '<p class="small muted" style="margin-top:12px">Ties are broken by signals we deliberately do not publish, and display alphabetically. Percentiles are computed internally; rank and raw score are what you see.</p>' +
      '</div>';
  }

  /* =======================================================================
     SCREEN — shortlists
     ===================================================================== */
  function slById(id) { return G.SHORTLISTS.filter(function (s) { return s.id === id; })[0]; }
  function unnotified(s) { return s.entries.filter(function (e) { return !e.notifiedAt; }); }
  function isOverdue(s) { return s.status === 'open' && daysFromToday(s.tentativeResultDate) < 0; }

  function statusPill(s) {
    if (s.status === 'draft') return '<span class="pill">' + icon(ICO.lock, 'ico--xs') + 'Draft · nothing sent</span>';
    if (s.status === 'closed') return '<span class="pill">Closed ' + relDays(s.closedAt) + '</span>';
    if (isOverdue(s)) return '<span class="pill pill--stale">' + icon(ICO.alert, 'ico--xs') + 'Past its date</span>';
    return '<span class="pill pill--primary"><span class="pill__dot"></span>Open</span>';
  }

  function renderShortlists() {
    var open = G.SHORTLISTS.filter(function (s) { return s.status === 'open'; });
    var overdueCount = open.filter(isOverdue).length;
    var overdueRatio = open.length ? overdueCount / open.length : 0;

    var flag = '';
    if (open.length && overdueRatio >= 0.8) {
      flag = '<div class="banner">' + icon(ICO.alert) +
        '<div class="banner__body"><b>' + overdueCount + ' of your ' + open.length + ' open rounds are past their tentative date (' +
        Math.round(overdueRatio * 100) + '%).</b> ' +
        'Nothing is blocked — a round can stall because a candidate went quiet. But at 80% or more, ' + esc(G.ORG.name) +
        ' is flagged to admins, because a pattern of opening rounds and never closing them wastes contributors’ time.</div></div>';
    } else if (overdueCount) {
      flag = '<div class="banner banner--info">' + icon(ICO.clock) +
        '<div class="banner__body"><b>' + overdueCount + ' of your ' + open.length + ' open rounds ' +
        (overdueCount === 1 ? 'is' : 'are') + ' past the date you promised (' + Math.round(overdueRatio * 100) + '%).</b> ' +
        'Nothing is blocked. At 80% or more, the organisation is notified and flagged to admins — so closing a finished round is worth doing.</div></div>';
    }

    screenEl.innerHTML =
      '<div class="page">' +
        '<div class="page-head">' +
          '<div>' +
            '<p class="eyebrow">' + esc(G.ORG.name) + ' · ' + G.ORG.members + ' members</p>' +
            '<h1 class="page-head__title">Shortlists</h1>' +
            '<p class="page-head__lede">Shortlists belong to the organisation, not to you — they survive someone leaving. Building one is private; sending it is deliberate and permanent.</p>' +
          '</div>' +
          '<div class="page-head__actions">' +
            '<button class="btn btn--primary" data-act="new-shortlist">' + icon(ICO.plus, 'ico--sm') + 'New shortlist</button>' +
          '</div>' +
        '</div>' +
        flag +
        '<div class="sl-grid">' +
          G.SHORTLISTS.map(function (s) {
            var notified = s.entries.filter(function (e) { return e.notifiedAt; });
            var accepted = s.entries.filter(function (e) { return e.contactStatus === 'accepted'; });
            return '<a class="card sl-card" href="#/shortlists/' + esc(s.id) + '">' +
              '<div class="sl-card__top">' +
                '<span class="od-field od-fill"><span class="sl-card__name">' + esc(s.name) + '</span>' +
                '<span class="entry__meta">Result date ' + dateLabel(s.tentativeResultDate) + '</span></span>' +
                statusPill(s) +
              '</div>' +
              '<p class="sl-card__desc od-clamp-2">' + esc(s.description) + '</p>' +
              '<div class="sl-card__foot">' +
                '<span class="sl-card__faces">' + s.entries.slice(0, 5).map(function (e) {
                  return avatar(G.BY_ID[e.userId], 'avatar--sm');
                }).join('') + '</span>' +
                '<span>' + s.entries.length + ' staged · <b class="mono">' + notified.length + '</b> notified · <b class="mono">' + accepted.length + '</b> accepted</span>' +
              '</div>' +
            '</a>';
          }).join('') +
        '</div>' +
      '</div>';
  }

  function contactPill(e) {
    if (!e.notifiedAt) return '<span class="pill">' + icon(ICO.lock, 'ico--xs') + 'Staged · nothing disclosed</span>';
    if (e.contactStatus === 'accepted') return '<span class="pill pill--primary">' + icon(ICO.check, 'ico--xs') + 'Accepted</span>';
    if (e.contactStatus === 'declined') return '<span class="pill pill--stale">Declined</span>';
    return '<span class="pill pill--violet">' + icon(ICO.clock, 'ico--xs') + 'Awaiting their answer</span>';
  }

  function renderShortlist(id) {
    var s = slById(id);
    if (!s) { renderNotFound(); return; }
    var pending = unnotified(s);
    var notified = s.entries.filter(function (e) { return e.notifiedAt; });

    screenEl.innerHTML =
      '<div class="page page--narrow">' +
        '<nav class="crumb" aria-label="Breadcrumb">' +
          '<a href="#/shortlists">' + icon(ICO.back, 'ico--xs') + ' Shortlists</a><span>/</span><span>' + esc(s.name) + '</span>' +
        '</nav>' +

        '<div class="page-head">' +
          '<div>' +
            '<h1 class="page-head__title">' + esc(s.name) + '</h1>' +
            '<p class="page-head__lede">' + esc(s.description) + '</p>' +
          '</div>' +
          '<div class="page-head__actions">' +
            (pending.length && s.status !== 'closed'
              ? '<button class="btn btn--primary" data-act="confirm-sl" data-sl="' + esc(s.id) + '">' + icon(ICO.send, 'ico--sm') +
                'Confirm ' + pending.length + ' · notify them' + '</button>'
              : '') +
            (s.status === 'open' ? '<button class="btn btn--ghost" data-act="close-sl" data-sl="' + esc(s.id) + '">Close the round</button>' : '') +
          '</div>' +
        '</div>' +

        '<div class="card card--pad" style="margin-bottom:var(--s2)">' +
          '<dl class="kv">' +
            '<dt>Status</dt><dd>' + statusPill(s) + '</dd>' +
            '<dt>Tentative result date</dt><dd>' + dateLabel(s.tentativeResultDate) +
              (isOverdue(s) ? ' <span class="pill pill--stale">' + Math.abs(daysFromToday(s.tentativeResultDate)) + ' days ago</span>' : '') + '</dd>' +
            '<dt>Owner</dt><dd>' + esc(G.ORG.name) + ' · created by ' + esc(s.createdBy) + ' ' + relDays(s.createdAt) + '</dd>' +
            '<dt>Contact policy</dt><dd>Email is released only if the contributor accepts.</dd>' +
          '</dl>' +
        '</div>' +

        (pending.length
          ? '<div class="notice" style="margin-bottom:var(--s2)">' +
              '<p class="notice__title">' + icon(ICO.lock, 'ico--sm') + pending.length + ' staged, nothing sent yet</p>' +
              '<p class="notice__body">Staged entries disclose nothing and can still be removed. Confirming tells each of them that ' + esc(G.ORG.name) +
              ' is interested — and that cannot be taken back.</p>' +
            '</div>'
          : '') +

        '<section class="card">' +
          (s.entries.length
            ? s.entries.map(function (e) { return entryRow(s, e); }).join('')
            : '<div class="empty" style="border:0;background:none">' +
              '<span class="empty__icon">' + icon(ICO.bookmark) + '</span>' +
              '<p class="empty__title">Nobody staged yet</p>' +
              '<p class="empty__body">Add candidates from search or from a scorecard. Nothing is disclosed until you confirm the round.</p>' +
              '<div class="empty__actions"><a class="btn btn--primary btn--sm" href="#/search">Go to search</a></div></div>') +
        '</section>' +

        (notified.length
          ? '<p class="small muted" style="margin-top:12px;max-width:76ch">You see statuses, and an email address only after someone accepts. ' +
            'Nothing tells a contributor who else is on this list.</p>'
          : '') +
      '</div>';
  }

  function entryRow(s, e) {
    var c = G.BY_ID[e.userId];
    var removable = !e.notifiedAt && s.status !== 'closed';
    return '<div class="entry">' +
      avatar(c, 'avatar--sm') +
      '<div class="od-field">' +
        '<a class="entry__name" href="#/contributors/' + esc(c.id) + '">' + esc(c.name) + '</a>' +
        '<span class="entry__meta">' +
          (c.rankOverall ? '#' + c.rankOverall + ' overall · ' : '') +
          n1(c.overall) + ' overall · added ' + relDays(e.addedAt) + ' by ' + esc(e.addedBy) +
          (e.email ? ' · <b class="mono">' + esc(e.email) + '</b>' : '') +
        '</span>' +
      '</div>' +
      '<span class="entry__status">' + contactPill(e) +
        (e.notifiedAt ? '<span class="entry__meta">Notified ' + relDays(e.notifiedAt) + '</span>' : '') +
      '</span>' +
      '<span class="entry__act">' +
        (removable
          ? '<button class="btn btn--ghost btn--sm" data-act="remove-entry" data-sl="' + esc(s.id) + '" data-user="' + esc(c.id) + '">' +
            icon(ICO.trash, 'ico--sm') + 'Remove</button>'
          : '<span class="pill">' + icon(ICO.lock, 'ico--xs') + 'Permanent</span>') +
      '</span>' +
    '</div>';
  }

  /* =======================================================================
     SCREEN — saved searches
     ===================================================================== */
  function describeFilters(f) {
    var bits = [];
    if (f.skills && f.skills.length) bits.push(f.skills.map(function (s) { return G.SKILL_BY_SLUG[s].name; }).join(' + '));
    if (f.min_skill_score) bits.push('skill ≥ ' + f.min_skill_score);
    if (f.min_overall_score) bits.push('overall ≥ ' + f.min_overall_score);
    if (f.min_generalist_score) bits.push('generalist ≥ ' + f.min_generalist_score);
    if (f.availability && f.availability.length) bits.push(f.availability.map(function (a) { return G.AVAILABILITY_LABEL[a]; }).join(' / '));
    if (f.evidence_within_months != null) bits.push('evidence within ' + f.evidence_within_months + ' months');
    if (f.include_inactive) bits.push('quiet profiles included');
    if (f.q) bits.push('name contains “' + f.q + '”');
    return bits.length ? bits.join(' · ') : 'No filters — the whole active pool';
  }

  function renderSaved() {
    screenEl.innerHTML =
      '<div class="page page--narrow">' +
        '<div class="page-head">' +
          '<div>' +
            '<p class="eyebrow">' + esc(G.ORG.name) + '</p>' +
            '<h1 class="page-head__title">Saved searches</h1>' +
            '<p class="page-head__lede">A saved search stores a question, never an answer. Replaying one runs it as you — so it can never show you someone your own permissions would hide.</p>' +
          '</div>' +
          '<div class="page-head__actions"><a class="btn btn--ghost" href="#/search">New search</a></div>' +
        '</div>' +

        (G.SAVED_SEARCHES.length
          ? '<div class="card">' + G.SAVED_SEARCHES.map(function (ss) {
              var f = parseQuery(toQuery(Object.assign({}, DEFAULTS, ss.filters)));
              var count = runSearch(f).total;
              return '<div class="entry">' +
                '<span class="avatar avatar--sm avatar--sq" data-tint="' + (ss.id.length % 8) + '" aria-hidden="true">' + icon(ICO.search, 'ico--sm') + '</span>' +
                '<div class="od-field">' +
                  '<span class="entry__name">' + esc(ss.name) + '</span>' +
                  '<span class="entry__meta">' + esc(describeFilters(ss.filters)) + '</span>' +
                  '<span class="entry__meta">Saved by ' + esc(ss.createdBy) + ' ' + relDays(ss.createdAt) + '</span>' +
                '</div>' +
                '<span class="entry__status"><span class="stat"><span class="stat__n">' + count + '</span>' +
                  '<span class="stat__label">matches now</span></span></span>' +
                '<span class="entry__act od-row" style="--od-gap:6px">' +
                  '<button class="btn btn--primary btn--sm" data-act="replay" data-ss="' + esc(ss.id) + '">Replay</button>' +
                  '<button class="btn btn--ghost btn--sm" data-act="delete-ss" data-ss="' + esc(ss.id) + '" aria-label="Delete ' + esc(ss.name) + '">' + icon(ICO.trash, 'ico--sm') + '</button>' +
                '</span>' +
              '</div>';
            }).join('') + '</div>'
          : '<div class="empty">' +
            '<span class="empty__icon">' + icon(ICO.bookmark) + '</span>' +
            '<p class="empty__title">No saved searches yet</p>' +
            '<p class="empty__body">Build a filter set on the search screen and save it. Everyone at ' + esc(G.ORG.name) + ' can replay it.</p>' +
            '<div class="empty__actions"><a class="btn btn--primary btn--sm" href="#/search">Go to search</a></div></div>') +
      '</div>';
  }

  /* =======================================================================
     Modals
     ===================================================================== */
  function openStageModal(userId) {
    var c = G.BY_ID[userId];
    var options = G.SHORTLISTS.filter(function (s) { return s.status !== 'closed'; });
    openModal(
      '<p class="eyebrow">Add to a shortlist</p>' +
      '<h2 class="modal__title" id="modal-title">' + esc(c.name) + '</h2>' +
      '<p class="small muted">Staging discloses nothing. ' + esc(c.name.split(' ')[0]) + ' is told only when you confirm the round.</p>' +
      '<div class="pick">' +
        (options.length ? '' :
          '<p class="small muted">Every round is closed. Open a new one to stage ' + esc(c.name.split(' ')[0]) + '.</p>') +
        options.map(function (s) {
          var already = s.entries.some(function (e) { return e.userId === userId; });
          return '<button class="pick__item" type="button" data-act="do-stage" data-sl="' + esc(s.id) + '" data-user="' + esc(userId) + '"' +
            (already ? ' disabled' : '') + '>' +
            '<span class="od-field"><span class="pick__name">' + esc(s.name) + '</span>' +
            '<span class="pick__meta">' + s.entries.length + ' staged · result date ' + dateLabel(s.tentativeResultDate) + '</span></span>' +
            (already ? '<span class="pill">Already on it</span>' : '<span class="pill pill--cherry">' + icon(ICO.plus, 'ico--xs') + 'Add</span>') +
          '</button>';
        }).join('') +
      '</div>' +
      '<div class="modal__foot">' +
        '<button class="btn btn--ghost" data-close="1">Cancel</button>' +
        '<button class="btn btn--primary" data-act="new-shortlist">' + icon(ICO.plus, 'ico--sm') + 'New shortlist</button>' +
      '</div>'
    );
  }

  function openNewShortlistModal(pendingUser) {
    var min = G.isoDaysAgo(-7);
    openModal(
      '<p class="eyebrow">New shortlist</p>' +
      '<h2 class="modal__title" id="modal-title">Open a round</h2>' +
      '<form id="sl-form" novalidate>' +
        '<div class="field" style="margin-bottom:var(--s2)">' +
          '<label class="field__label" for="sl-name">Name <span class="muted">· required</span></label>' +
          '<input class="input" id="sl-name" required placeholder="e.g. Platform team — Q1 hires" />' +
          '<p class="field__help" id="sl-name-err" hidden style="color:var(--cherry)">Give the round a name so your colleagues know what it is for.</p>' +
        '</div>' +
        '<div class="field" style="margin-bottom:var(--s2)">' +
          '<label class="field__label" for="sl-desc">What this round is</label>' +
          '<input class="input" id="sl-desc" placeholder="Role, seniority, how you will run it" />' +
        '</div>' +
        '<div class="field">' +
          '<label class="field__label" for="sl-date">Tentative result date <span class="muted">· required</span></label>' +
          '<input class="input" id="sl-date" type="date" min="' + min + '" value="' + G.isoDaysAgo(-30) + '" required />' +
          '<p class="field__help">Every contributor you contact is told this date. It is the commitment that makes the request worth answering.</p>' +
        '</div>' +
      '</form>' +
      '<div class="modal__foot">' +
        '<button class="btn btn--ghost" data-close="1">Cancel</button>' +
        '<button class="btn btn--primary" data-act="do-create-sl" data-user="' + esc(pendingUser || '') + '">Create shortlist</button>' +
      '</div>'
    );
  }

  function openConfirmModal(slId) {
    var s = slById(slId);
    var list = unnotified(s);
    openModal(
      '<p class="eyebrow">Confirm the round</p>' +
      '<h2 class="modal__title" id="modal-title">Notify ' + list.length + ' ' + (list.length === 1 ? 'contributor' : 'contributors') + '</h2>' +
      '<div class="notice">' +
        '<p class="notice__title">' + icon(ICO.alert, 'ico--sm') + 'This cannot be undone</p>' +
        '<p class="notice__body">Once someone has been told that ' + esc(G.ORG.name) + ' is interested, that is a fact. ' +
        'These entries become permanent and can no longer be removed. Anyone you stage later is still removable until the next confirm.</p>' +
      '</div>' +
      '<div class="pick">' + list.map(function (e) {
        var c = G.BY_ID[e.userId];
        return '<div class="pick__item" style="cursor:default">' +
          '<span class="od-row" style="--od-gap:10px">' + avatar(c, 'avatar--sm') +
          '<span class="od-field"><span class="pick__name">' + esc(c.name) + '</span>' +
          '<span class="pick__meta">' + (c.rankOverall ? '#' + c.rankOverall + ' overall · ' : '') + n1(c.overall) + ' overall</span></span></span>' +
          '<span class="pill pill--violet">Will be emailed</span></div>';
      }).join('') + '</div>' +
      '<div class="card card--pad" style="background:var(--surface-2)">' +
        '<p class="eyebrow" style="margin-bottom:8px">What each of them will read</p>' +
        '<p class="small"><b>' + esc(G.ORG.name) + '</b> has shortlisted you for <b>' + esc(s.name) + '</b>. ' +
        'They expect to have an answer for you by <b>' + dateLabel(s.tentativeResultDate) + '</b>. ' +
        'Your email address is released only if you accept.</p>' +
        (G.ORG.paymentVerifiedAt == null
          ? '<p class="small" style="margin-top:8px;color:var(--amber)"><em>This organisation is hiring for the first time and has not verified payment capability.</em></p>' +
            '<p class="field__help" style="margin-top:8px">We disclose this because the person deciding whether to hand over their email is the one carrying the risk of an unpaid engagement.</p>'
          : '') +
      '</div>' +
      '<div class="modal__foot">' +
        '<button class="btn btn--ghost" data-close="1">Not yet</button>' +
        '<button class="btn btn--danger" data-act="do-confirm" data-sl="' + esc(slId) + '">' + icon(ICO.send, 'ico--sm') + 'Send ' + list.length + ' · permanent</button>' +
      '</div>'
    );
  }

  function openCloseModal(slId) {
    var s = slById(slId);
    var waiting = s.entries.filter(function (e) { return e.contactStatus === 'pending'; }).length;
    openModal(
      '<p class="eyebrow">Close the round</p>' +
      '<h2 class="modal__title" id="modal-title">' + esc(s.name) + '</h2>' +
      '<p class="small">Closing stamps the round as finished and stops it counting towards your overdue ratio. ' +
      'It does not un-tell anybody: every contact request already sent stays on the record.</p>' +
      (waiting
        ? '<div class="notice"><p class="notice__title">' + icon(ICO.clock, 'ico--sm') + waiting + ' still waiting to hear back</p>' +
          '<p class="notice__body">They were promised an answer by ' + dateLabel(s.tentativeResultDate) +
          '. Closing the round here does not send them one.</p></div>'
        : '') +
      '<div class="modal__foot">' +
        '<button class="btn btn--ghost" data-close="1">Keep it open</button>' +
        '<button class="btn btn--primary" data-act="do-close" data-sl="' + esc(slId) + '">Close the round</button>' +
      '</div>'
    );
  }

  function openSaveSearchModal(filters) {
    var count = runSearch(filters).total;
    openModal(
      '<p class="eyebrow">Save this search</p>' +
      '<h2 class="modal__title" id="modal-title">Store the question</h2>' +
      '<p class="small muted">' + esc(describeFilters(filters)) + ' · <b>' + count + '</b> ' + (count === 1 ? 'match' : 'matches') + ' right now.</p>' +
      '<div class="field">' +
        '<label class="field__label" for="ss-name">Name <span class="muted">· required</span></label>' +
        '<input class="input" id="ss-name" required placeholder="e.g. Go + Kubernetes, ranked depth" />' +
        '<p class="field__help" id="ss-name-err" hidden style="color:var(--cherry)">Name it so a colleague knows what they are replaying.</p>' +
      '</div>' +
      '<div class="modal__foot">' +
        '<button class="btn btn--ghost" data-close="1">Cancel</button>' +
        '<button class="btn btn--primary" data-act="do-save-search">Save for ' + esc(G.ORG.name) + '</button>' +
      '</div>',
      function (modal) { modal.__filters = filters; }
    );
  }

  /* =======================================================================
     Actions
     ===================================================================== */
  function stage(slId, userId) {
    var s = slById(slId);
    if (s.entries.some(function (e) { return e.userId === userId; })) return;
    s.entries.push({
      userId: userId, addedAt: G.isoDaysAgo(0), addedBy: G.HIRER.name,
      notifiedAt: null, contactStatus: null, emailReleasedAt: null, email: null
    });
    render();
    closeModal();
    toast('<b>' + esc(G.BY_ID[userId].name) + '</b> staged on “' + esc(s.name) + '”. Nothing has been sent — you can still remove them.');
  }

  function removeEntry(slId, userId) {
    var s = slById(slId);
    var e = s.entries.filter(function (x) { return x.userId === userId; })[0];
    if (!e || e.notifiedAt) return;
    s.entries = s.entries.filter(function (x) { return x.userId !== userId; });
    toast('Removed ' + esc(G.BY_ID[userId].name) + ' — they were never told.', 'plain');
    render();
  }

  function confirmRound(slId) {
    var s = slById(slId);
    var list = unnotified(s);
    list.forEach(function (e) {
      e.notifiedAt = G.isoDaysAgo(0);
      e.contactStatus = 'pending';
    });
    s.status = 'open';
    render();
    closeModal();
    toast('<b>' + list.length + ' contact ' + (list.length === 1 ? 'request' : 'requests') + ' sent.</b> Those entries are permanent now; their email arrives only if they accept.');
  }

  function closeRound(slId) {
    var s = slById(slId);
    s.status = 'closed';
    s.closedAt = G.isoDaysAgo(0);
    toast('“' + esc(s.name) + '” closed. Everyone who was notified keeps their record of it.', 'plain');
    render();
  }

  /* =======================================================================
     Router
     ===================================================================== */
  var scrollMemory = {};
  var currentKey = null;

  function currentQuery() {
    return filterOverride != null ? filterOverride : routeOf().query;
  }

  function routeOf() {
    var h = location.hash.replace(/^#/, '') || '/search';
    var qi = h.indexOf('?');
    return { path: qi < 0 ? h : h.slice(0, qi), query: qi < 0 ? '' : h.slice(qi + 1) };
  }

  function render() {
    var r = routeOf();
    var parts = r.path.split('/').filter(Boolean);
    var nav = 'search';

    if (parts[0] === 'contributors' && parts[1]) { renderScorecard(decodeURIComponent(parts[1])); nav = 'search'; }
    else if (parts[0] === 'leaderboard') { renderLeaderboard(); nav = 'leaderboard'; }
    else if (parts[0] === 'shortlists' && parts[1]) { renderShortlist(decodeURIComponent(parts[1])); nav = 'shortlists'; }
    else if (parts[0] === 'shortlists') { renderShortlists(); nav = 'shortlists'; }
    else if (parts[0] === 'saved-searches') { renderSaved(); nav = 'saved'; }
    else { renderSearch(filterOverride != null ? filterOverride : r.query); nav = 'search'; }

    document.querySelectorAll('.nav__item').forEach(function (a) {
      if (a.getAttribute('data-nav') === nav) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    });
    document.querySelector('[data-nav-count="shortlists"]').textContent = G.SHORTLISTS.length;
    document.querySelector('[data-nav-count="saved"]').textContent = G.SAVED_SEARCHES.length;
    document.title = titleFor(parts) + ' — GitCherryPick';
  }

  function titleFor(parts) {
    if (parts[0] === 'contributors' && G.BY_ID[parts[1]]) return G.BY_ID[parts[1]].name;
    if (parts[0] === 'leaderboard') return 'Leaderboard';
    if (parts[0] === 'shortlists' && parts[1]) { var s = slById(parts[1]); return s ? s.name : 'Shortlist'; }
    if (parts[0] === 'shortlists') return 'Shortlists';
    if (parts[0] === 'saved-searches') return 'Saved searches';
    return 'Search';
  }

  /* Back restores where you were, not the top of the page. */
  window.addEventListener('hashchange', function () {
    filterOverride = null;
    if (currentKey) scrollMemory[currentKey] = window.scrollY;
    var r = routeOf();
    render();
    var key = r.path;
    var y = scrollMemory[key];
    window.scrollTo({ top: y || 0, behavior: 'auto' });
    currentKey = key;
    if (!y) screenEl.focus({ preventScroll: true });
  });
  window.addEventListener('scroll', function () {
    if (currentKey) scrollMemory[currentKey] = window.scrollY;
  }, { passive: true });

  /* ---------------------------------------------------- global delegation */
  document.addEventListener('click', function (ev) {
    var b = ev.target.closest('[data-act]');
    if (!b || b.disabled) return;
    var act = b.getAttribute('data-act');

    if (act === 'toggle-ev') {
      var body = document.getElementById('ev-' + b.getAttribute('data-ev'));
      var open = b.getAttribute('aria-expanded') === 'true';
      b.setAttribute('aria-expanded', String(!open));
      body.hidden = open;
      return;
    }
    if (act === 'reset') { goSearch(JSON.parse(JSON.stringify(DEFAULTS)), true); return; }
    if (act === 'show-inactive') {
      var fi = parseQuery(currentQuery()); fi.include_inactive = true; fi.page = 1;
      goSearch(fi, true); return;
    }
    if (act === 'relax') {
      var fr = parseQuery(currentQuery());
      fr.min_skill_score = 0; fr.min_overall_score = 0; fr.min_generalist_score = 0; fr.page = 1;
      goSearch(fr, true); return;
    }
    if (act === 'page') {
      var fp = parseQuery(currentQuery()); fp.page = Number(b.getAttribute('data-page'));
      goSearch(fp, true);
      window.scrollTo({ top: 0, behavior: 'smooth' });
      return;
    }
    if (act === 'save-search') { openSaveSearchModal(parseQuery(currentQuery())); return; }
    if (act === 'board') { boardState.kind = b.getAttribute('data-kind'); render(); return; }
    if (act === 'stage') { openStageModal(b.getAttribute('data-user')); return; }
    if (act === 'do-stage') { stage(b.getAttribute('data-sl'), b.getAttribute('data-user')); return; }
    if (act === 'new-shortlist') {
      var pending = overlayHost.querySelector('[data-act="do-stage"]');
      var user = b.getAttribute('data-user') || (pending ? pending.getAttribute('data-user') : '');
      openNewShortlistModal(user);
      return;
    }
    if (act === 'do-create-sl') {
      var name = document.getElementById('sl-name');
      var date = document.getElementById('sl-date');
      var err = document.getElementById('sl-name-err');
      if (!name.value.trim()) { err.hidden = false; name.focus(); return; }
      if (!date.value) { date.focus(); return; }
      var id = 'sl-' + Date.now();
      G.SHORTLISTS.unshift({
        id: id, name: name.value.trim(),
        description: document.getElementById('sl-desc').value.trim() || 'No description yet.',
        status: 'draft', tentativeResultDate: date.value,
        createdAt: G.isoDaysAgo(0), createdBy: G.HIRER.name, closedAt: null, entries: []
      });
      var u = b.getAttribute('data-user');
      if (u) { stage(id, u); return; }
      closeModal();
      toast('“' + esc(name.value.trim()) + '” created as a draft. Stage candidates from search — nothing is sent until you confirm.');
      location.hash = '#/shortlists/' + id;
      return;
    }
    if (act === 'remove-entry') { removeEntry(b.getAttribute('data-sl'), b.getAttribute('data-user')); return; }
    if (act === 'confirm-sl') { openConfirmModal(b.getAttribute('data-sl')); return; }
    if (act === 'do-confirm') { confirmRound(b.getAttribute('data-sl')); return; }
    if (act === 'close-sl') { openCloseModal(b.getAttribute('data-sl')); return; }
    if (act === 'do-close') { closeModal(); closeRound(b.getAttribute('data-sl')); return; }
    if (act === 'do-save-search') {
      var ssName = document.getElementById('ss-name');
      var ssErr = document.getElementById('ss-name-err');
      if (!ssName.value.trim()) { ssErr.hidden = false; ssName.focus(); return; }
      var f = overlayHost.querySelector('.modal').__filters;
      G.SAVED_SEARCHES.unshift({
        id: 'ss-' + Date.now(), name: ssName.value.trim(),
        createdAt: G.isoDaysAgo(0), createdBy: G.HIRER.name,
        filters: JSON.parse(JSON.stringify(f))
      });
      closeModal();
      toast('Saved for ' + esc(G.ORG.name) + '. Replaying it runs the question as whoever opens it.');
      render();
      return;
    }
    if (act === 'replay') {
      var ss = G.SAVED_SEARCHES.filter(function (x) { return x.id === b.getAttribute('data-ss'); })[0];
      var merged = Object.assign({}, DEFAULTS, ss.filters);
      location.hash = '#/search' + (toQuery(merged) ? '?' + toQuery(merged) : '');
      return;
    }
    if (act === 'delete-ss') {
      var sid = b.getAttribute('data-ss');
      G.SAVED_SEARCHES = G.SAVED_SEARCHES.filter(function (x) { return x.id !== sid; });
      toast('Saved search deleted.', 'plain');
      render();
      return;
    }
  });

  document.addEventListener('change', function (ev) {
    if (ev.target.getAttribute && ev.target.getAttribute('data-act') === 'board-skill') {
      boardState.skill = ev.target.value;
      render();
    }
  });

  /* ------------------------------------------------------------- boot */
  if (!location.hash) location.hash = '#/search';
  currentKey = routeOf().path;
  render();
})();
