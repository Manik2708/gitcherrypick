# Hiring console — design reference

A static, runnable prototype of the **hirer-facing** surface: search, scorecard,
leaderboard, shortlists, and saved searches. Open `index.html`. No build step, no
dependencies, no network.

**This is not stage 6.** It is a design reference that sits beside the React app, not
the React app. Stage 6 (`frontend/`) is still gated behind stages 4 and 5 and nothing
here starts it. Treat this the way you treat `rfc/` — a document that happens to run.

## What it covers

| Route | Screen | Backing spec |
|---|---|---|
| `#/search` | Discovery — skill AND filters, score thresholds, availability, recency, name lookup | ADR-0008 §1, §1a, §1b |
| `#/contributors/{id}` | Full scorecard — standings, per-PR-per-skill breakdown, evidence | ADR-0008 §2 |
| `#/leaderboard` | Overall, generalist, and per-skill boards | ADR-0008 §1 |
| `#/shortlists` | Org-scoped rounds, tentative dates, overdue ratio | ADR-0005, ADR-0008 §3 |
| `#/shortlists/{id}` | Stage → confirm, contact-request statuses | ADR-0008 §3a, §4 |
| `#/saved-searches` | Store the question, replay as the caller | ADR-0008 §5 |

## What it gets right, deliberately

Three rules that are easy to lose in implementation, so the prototype makes them visible:

- **Rank is global.** Filtering does not recompute it, so a filtered list has *gaps* in
  the rank sequence and reports `inactive_hidden` beside the toggle that fills them.
  Any client that assumes `results[i].rank == i + 1` is wrong (ADR-0008 §Consequences).
- **Score and standing are orthogonal.** A secondary skill scoring 81 renders unranked
  next to a primary scoring 62. No `skills=` filter would ever have found it; the
  scorecard shows it anyway.
- **Staging discloses nothing.** Confirm is the disclosure, it is irreversible, and the
  dialog says so — including the unverified-payment line when
  `hirer_accounts.payment_verified_at IS NULL`.

## The numbers are computed, not authored

`assets/js/data.js` implements the ADR-0005 arithmetic directly: log/percentile
normalisation against `global_norms`, `Q` from the five weighted judged dimensions,
project reach `R`, engagement `E`, then `PR_score`, `Skill_score`, and both user-level
numbers. A breakdown on screen always adds up to the score printed beside it. If a
constant moves in the rubric, change it here and every dependent number follows.

`pr-review` is wired as `judged_only` — no PR-level arithmetic, reach retained, ω = 1.2
capped at 100 (ADR-0007).

## Fixtures and honesty about the data

- Contributors are **fictional**. The four from `backend/e2e/fixtures/seed/` (Alice, Bob,
  Carol, Dave) are present and behave as their fixtures describe — Carol is lapsed, Dave
  holds Go as secondary at four PRs. Avatars are generated monograms; no real person's
  photograph is attached to an invented score.
- Repositories are **real**, with their real org marks localised under
  `assets/img/repos/`. Star, fork, and dependent counts are approximate sample values,
  not verified platform facts.
- The dataset is in-memory and shaped to the ADR-0008 response bodies. Staging,
  confirming, and saving a search persist for the session and reset on reload.

## Files

```
index.html              entry point, app shell, layout primitives layer
assets/css/app.css      design tokens + every component; change a token, not a rule
assets/js/data.js       seeded population + the scoring arithmetic
assets/js/app.js        router and the six screens
assets/fonts/           IBM Plex Sans, IBM Plex Mono, Instrument Serif (woff2, local)
assets/img/repos/       19 GitHub org marks
```

Design tokens live once on `:root` in `app.css` — palette, type scale, 8px spacing
rhythm, radii, motion durations. Nothing is hard-coded at the call site.
