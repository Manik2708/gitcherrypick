# RFC-0021 — One availability switch

**Status:** Approved 2026-09-20 · **Binding form:** [ADR-0021](../adr/ADR-0021-one-availability-switch.md)
· **Schema:** [`RFC-0021-*.schema`](RFC-0021-one-availability-switch.schema)
· **Amends:** ADR-0005, ADR-0008 §1, ADR-0010 §5, ADR-0018 §2, ADR-0019, ADR-0020

## Summary

A contributor currently answers two overlapping questions. `availability_status` asks
whether they are looking, in four values — `not_looking`, `looking_for_job`,
`looking_for_freelance`, `open_to_freelance` — and the `OpenTo*` flags ask what shape of
work they would take, in four booleans that do not include freelance.

Three of the four statuses say the same thing: yes. They differ only in what **kind** of
work the person wants, which is the flags' question.

So: **one switch, "Looking for opportunities"**, and **freelance joins the flags**. What
somebody is looking for stops being said in two vocabularies and is said in one.

## 1. Why the overlap was load-bearing, and why removing it is the fix

ADR-0018 §2 refused an `open_to_freelance` flag, and the reason was right:

> `availability_status` already carries freelance twice over, and two controls that both
> mean "freelance" can disagree — leaving a UI to decide which wins, differently in each
> place somebody reads it.

That reasoning stands. What it does not settle is **which** of the two should go. Keeping
the status and refusing the flag left freelance as the one engagement expressed differently
from the other four — so ADR-0019 had to special-case it when mapping a role to a search,
ADR-0020 had to special-case it again in the opening filter, and the frontend had to
special-case it a third time. Three places carrying an exception, to avoid a duplication
that could have been removed from the other end.

Removing it from the **status** resolves it the same way and leaves one vocabulary. The
flags already compose, which an enum cannot: somebody open to a remote contract and to
freelance work is stating two things, and the status made them choose.

## 2. The switch decides findability. The shapes are a preference.

**Decided, and it reverses ADR-0018 §4.**

A contributor is **looking** or **not looking**. Looking means open to anything — freelance,
remote, an internship, whatever. Not looking means open to nothing. That is the whole of
what decides whether a hirer can reach them.

The shapes — remote, onsite, contract, internship, **freelance** — stop gating discovery and
become what somebody would **prefer**. They filter which openings that contributor is shown,
and a hirer reads them as a stated preference rather than as consent.

### Why the composition rule goes

ADR-0018 §4 made availability and the flags compose: available for the shapes you ticked and
for nothing else. It was defensible and it produced a trap the ADR then had to warn about at
length — a live window with nothing ticked matched **nobody**, invisible from outside, which
ADR-0018 §The unmatchable state exists entirely to explain, and which the client has to
detect and put a banner in front of.

It also made saying "yes, I am looking" insufficient. Somebody who answered the question
they were asked was still unreachable until they answered a second one, and nothing on the
screen made the second one feel required.

Under one switch that state cannot occur. **The unmatchable state stops being a thing** — the
warning, the `matchable` computation behind it and the banner all delete themselves, which is
the same argument as §5 below: this change removes code.

### What a hirer's `open_to` filter now means

A stated preference, not a boundary. A hirer filtering `open_to=contract` is asking for
people who said they would take contract work; they may still approach somebody about a
permanent role, exactly as they may approach somebody whose stated country is not the
role's. The consent that matters is unchanged and is where it always was: the contact
request, which the contributor answers.

## 3. Three states, not two

**Decided: the opt-out is unchanged.** `availability_status` stays an enum of `not_looking`
and `looking` rather than becoming a boolean, because the column has to hold three
distinguishable things — and because a contributor who deliberately opts out keeps the
guarantee they have today, that no hirer can surface them by ticking a box:

| state             | meaning                                               | visible by default   |
| ----------------- | ----------------------------------------------------- | -------------------- |
| no row            | never answered                                        | no                   |
| `not_looking`     | **opted out** — no toggle reveals them (ADR-0008 §1a) | no                   |
| `looking`, lapsed | said yes, did not refresh                             | no, until the toggle |
| `looking`, live   | said yes                                              | yes                  |

The 15-day window is untouched (ADR-0010 §5). It is what stops the pool filling with people
who stopped looking a year ago, and collapsing the values has nothing to do with it.

## 4. The `availability` search filter goes

A hirer could filter on `availability=looking_for_freelance`. With one looking state that
filter can only say "looking", which every default search result already is — so it would
be a control that does nothing.

What it was really for is now `open_to=freelance`, in the same list as the other four
shapes. The distinction moved to where the roles already speak.

`include_inactive` is unchanged: it reveals people whose window lapsed, and still never
reveals `not_looking`.

## 5. Three special cases delete themselves

- **ADR-0019** mapped a freelance role to `availability` instead of `open_to`, because
  there was no flag. It maps to `open_to=freelance` like every other engagement.
- **ADR-0020**'s opening filter keyed freelance off `availability_status` while every other
  engagement read a flag. One `CASE` arm, gone.
- The client's role-to-filter mapping carried the same exception a third time.

Each was a correct workaround for the duplication. None is needed once the duplication is
gone, and that is the strongest argument for this change: it removes code rather than
adding it.

## Consequences

- `availability_status` loses three values; every fixture asserting one is rewritten. The
  values were in ~40 files, which is the real cost of this RFC.
- A contributor who had `looking_for_freelance` becomes `looking` **and** should have
  `open_to_freelance` ticked. Nothing is deployed, so this is a seed change rather than a
  migration — but if that ever stops being true, it is a backfill with a judgement call in
  it, and the judgement should be made before then rather than during.
- **More contributors become reachable**, immediately. Anybody who said they were looking
  and ticked nothing was invisible and is now findable. That is the intended effect and it
  is also the one worth watching: the pool grows without anybody having opted into
  anything new, so the first search after this lands will look different.
- The `availability` filter is removed from search and from `saved_searches.filters`. A
  saved search holding one is refused as an unknown key, which is the existing behaviour
  for a stale parameter and tells the owner their saved question no longer means what it
  did.

## Open questions

None. Resolved with the owner on 2026-09-20: the opt-out is unchanged (§3), and the switch
alone decides findability while the shapes become a preference (§2). A contributor who is
`looking_for_freelance` today becomes `looking` with `open_to_freelance` ticked, which in the
seeds is a rewrite rather than a migration.
