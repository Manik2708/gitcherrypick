# RFC-0020 — Public openings

**Status:** Approved 2026-09-19 · **Binding form:** [ADR-0020](../adr/ADR-0020-public-openings.md)
· **Schema:** [`RFC-0020-*.schema`](RFC-0020-public-openings.schema)
· **Depends on:** [ADR-0005](../adr/ADR-0005-scoring-and-discovery.md),
[ADR-0007](../adr/ADR-0007-rubric-contract-and-generalist-score.md),
[ADR-0018](../adr/ADR-0018-contributor-profile.md),
[ADR-0019](../adr/ADR-0019-roles.md)

## Summary

Every role so far is **private**. A hirer searches, shortlists, and the contributor hears
about the job only when a contact request lands. A contributor cannot see what exists.

An opening is a role a company has chosen to publish, with a **bar** on it: a minimum
overall score, a minimum generalist score, per-skill minimums, and a minimum number of
years contributing. A contributor browsing openings sees only the ones they meet.

It inverts discovery in one direction and **nothing else**. There is no apply button, no way
for a contributor to make themselves known by reading one, and no signal to the company that
anybody looked. A company still approaches; the address is still released only on acceptance
(ADR-0005 §9). What changes is that somebody can see what is out there.

## 1. On top of a role, not inside one

`role_openings` references a role and holds the bar. It is not columns on `roles`, for a
reason that is structural rather than tidy: **an open role is immutable** (ADR-0019 §13).
Publishing, withdrawing and re-pitching a public advert all have to be possible without
rewriting the job underneath — and if the bar lived on the role, raising a score threshold
would mean superseding the role and closing it under everybody already contacted.

One opening per role, enforced by a unique key. Two would be two public descriptions of one
job, each with its own bar, and nothing able to say which a contributor is looking at.

## 2. What the bar is, and what it is not

| field                  | compared against                                         |
| ---------------------- | -------------------------------------------------------- |
| `min_overall_score`    | `users.overall_score` (0–100, depth)                     |
| `min_generalist_score` | `users.generalist_score` (unbounded, breadth)            |
| per-skill `min_score`  | `user_skills.score` at **primary** standing              |
| `min_oss_yoe`          | verified years since the first pull request was authored |

Countries are **not** repeated here. The role already states where it can hire, and a second
list that could disagree with the first is the bug (ADR-0019 §5). The filter reads the role's.

Per-skill bars match **primary standing only**, exactly as search does (ADR-0005). Five
distinct merged pull requests is what makes a skill rankable; a bar cleared by a secondary
skill would be cleared by evidence the platform declines to rank.

**A stated bar is not cleared by an absent score.** A contributor who has never submitted a
claim has no overall score, and showing them a role asking for 60 would promise a match that
does not exist. The remedy is to submit a claim, not for the platform to look away. This is
the same fail-closed rule verified open-source years already follow (ADR-0019 §7), and the
opposite of how `office_yoe` behaves — because that figure is self-reported and these are
derived from evidence.

## 3. What a contributor sees

`GET /openings`, contributor-only, returning live openings whose bar they clear and whose
role can hire where they are.

They see the role as the **contact-request shape** already defined by ADR-0019 §2 — title,
engagement, location, pay, countries, the process fields — plus the organisation's name, and
the bar itself. Not `status`, not the supersede chain, not `hires`, which names other
contributors.

**Decided: only what they clear, plus a bare count.** The list holds the openings a
contributor meets. Underneath it, one line: _"4 open to you · 11 you do not currently meet."_

The count is the whole of the compromise. An empty list on its own lies about why it is
empty — a contributor cannot tell whether nobody is hiring or whether they clear nothing —
and the number answers that in the only way that costs nobody anything. It itemises no
shortfall, so nobody is handed a list of jobs they cannot have annotated with how far off
they are, and it reveals no company's bar, so an opening cannot be reverse-engineered into
a public account of who that company will take.

**The bar is shown on the ones they see.** They have already cleared it, so it discloses
nothing they could not infer, and a role that states what it wanted is more legible than one
that simply appeared.

**And there is no apply button — decided, and load-bearing.** Reading an opening sends
nothing to the company: no application, no interest, no view count, no signal of any kind.
The consent model runs one way (ADR-0005 §9) — companies approach, contributors answer — and
an apply button would be a second channel with none of the protections the first one has.
A contributor's route into a hiring conversation is still to be found, and what an opening
changes is only that they can now see what exists.

## 4. What it does not do

- **No applications** (§3). The consent model runs one way: companies approach,
  contributors answer.
- **No disclosure to the company.** Reading an opening sends nothing. A company cannot see
  who looked, and there is deliberately no counter — a view count would become a ranking
  signal nobody consented to produce.
- **No new search surface for hirers.** Openings are a contributor-facing read.

## 5. Publishing is a separate act, under the existing authority

Creating an opening drafts it; publishing makes it live. That mirrors a role's own
`draft → open` split and for the same reason: an opening states a public bar on the
company's behalf, which is the kind of commitment ADR-0019 §11 put behind an owner.

It reuses `role_create_authority` rather than adding a fourth setting. A fourth dial for a
thing that is published alongside the role it describes would be a setting nobody sets.

Only an **open** role may have a published opening. A draft is not a commitment and a closed
role is a withdrawn one, so neither is a job anybody should be reading about.

## Endpoints

| Method | Path                                                 | Who                                                          |
| ------ | ---------------------------------------------------- | ------------------------------------------------------------ |
| PUT    | `/org-roles/{orgID}/roles/{roleID}/opening`          | per `role_create_authority` — creates or edits the draft bar |
| POST   | `/org-roles/{orgID}/roles/{roleID}/opening/publish`  | per `role_create_authority`                                  |
| POST   | `/org-roles/{orgID}/roles/{roleID}/opening/withdraw` | per `role_close_authority`                                   |
| GET    | `/org-roles/{orgID}/roles/{roleID}/opening`          | any org hirer                                                |
| GET    | `/openings`                                          | contributor — only what they clear                           |

## Consequences

- The platform gets a **public face** for the first time. Everything before this was visible
  only to a verified hirer or to the person it was about.
- A contributor now has a concrete reason to submit a claim: without scores they clear no
  bar that states one.
- `GET /openings` is the first query that filters people's own scores against a company's
  threshold. It reads `user_skills` per opening, so it is the one read here worth watching
  as the catalogue grows.
- Withdrawing is a stamp, not a delete, so the advert history survives.

## 6. An opening lives exactly as long as its role

**Decided: no expiry of its own.** An opening is published until the role closes, and
closing the role takes the opening with it — the foreign key cascades and the contributor
read requires `roles.status = 'open'`, so a withdrawn job cannot leave an advert behind.

**And its age is the ROLE'S age**, not the date somebody got round to publishing it. The job
has existed since the role opened; a company that advertised late would otherwise look like
it had a fresher opening than one that published immediately, which rewards the wrong thing.
`opened_at` is what a contributor is shown.

This keeps the asymmetry ADR-0019 already accepted — a contributor must keep saying they are
available, a company need not keep saying it is hiring — and makes it public, which is the
part worth naming. The mitigation is that the age is stated plainly rather than hidden, so
staleness costs the company its own credibility instead of costing the contributor their
attention silently. If adverts start rotting, the fix is the flagging mechanism ADR-0005 §10
already computes, not a second clock.

## Open questions

None. All three were resolved with the owner on 2026-09-19: only what a contributor clears
plus a bare count (§3), no apply button (§3), and no expiry of an opening's own (§6).
