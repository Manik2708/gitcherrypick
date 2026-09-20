# ADR-0021 — One availability switch

**Status:** Accepted · **From:** [RFC-0021](../rfc/RFC-0021-one-availability-switch.md)
· **Schema:** [`RFC-0021-*.schema`](../rfc/RFC-0021-one-availability-switch.schema)
· **Amends:** ADR-0005, ADR-0008 §1, ADR-0010 §5, ADR-0018 §2 and §4, ADR-0019, ADR-0020

## Decision

1. **A contributor is `looking` or `not_looking`.** The four-value status collapses: three of
   them said "yes" and differed only in what kind of work, which is the flags' question.
2. **The switch alone decides findability.** Looking means open to anything — freelance,
   remote, an internship. Not looking means open to nothing.
3. **The shapes stop gating discovery and become a PREFERENCE.** Remote, onsite, contract,
   internship and now **freelance**. They filter which openings a contributor is shown, and
   a hirer reads them as a stated preference rather than as consent.
4. **ADR-0018 §4 is reversed**, and the unmatchable state it warned about ceases to exist. A
   live window with nothing ticked used to match nobody while looking fine from the inside;
   it cannot happen now, so the warning, the `Matchable` computation and the banner all go.
5. **`open_to_freelance` is added**, reversing ADR-0018 §2. That decision's reasoning was
   sound — availability carried freelance twice over, so a flag would have been a third
   place to say it — and is spent, because availability no longer says it at all.
6. **`not_looking` is unchanged**: a deliberate opt-out that no toggle reveals (ADR-0008
   §1a). The column holds three states — no row, opted out, and looking-but-lapsed — which
   is why it stays an enum rather than becoming a boolean.
7. **The 15-day window is unchanged** (ADR-0010 §5). It is what stops the pool filling with
   people who stopped looking a year ago, and collapsing the values does not touch it.
8. **The `availability` search filter is removed.** With one looking state it could only say
   "looking", which every default result already is. What it was for is now
   `open_to=freelance`, in the same vocabulary as the other four shapes.

## Implementation

### What a hirer's `open_to` filter means now

A stated preference, not a boundary. Filtering `open_to=contract` asks for people who said
they would take contract work; a hirer may still approach somebody about a permanent role,
exactly as they may approach somebody whose stated country is not the role's. The consent
that matters is unchanged and is where it always was — the contact request.

### Three special cases delete themselves

Freelance was the one engagement expressed differently from the other four, so three places
carried an exception for it: ADR-0019's role-to-search mapping, ADR-0020's opening filter,
and the client's copy of the same mapping. Each was a correct workaround for the duplication
and none is needed now.

## Steps

1. **Schema.** Two edits at origin: `availability_status` in RFC-0002 loses three values,
   and `user_work_preferences` in RFC-0018 gains `open_to_freelance`. No `ALTER` — nothing
   is deployed and `rfc/*.schema` applied in order is the schema.
2. **Domain.** The status constants, and `Matchable` deleted.
3. **Repositories.** Search drops the availability filter and the shape gate; the opening
   filter drops its freelance `CASE` arm and keeps the shapes as the contributor's own
   preference over their own view.
4. **Services.** Profile stops computing `Matchable`; contact acceptance still refreshes.
5. **Controllers.** `availability` leaves `knownFilters`; `/me/profile` gains the fifth flag.
6. **Frontend.** One switch in place of four buttons, freelance in the shape list, and the
   unmatchable banner removed.
7. **Fixtures.** Every assertion naming one of the three dropped values.

## Consequences

- **More contributors become reachable immediately.** Anybody who said they were looking and
  ticked nothing was invisible and is now findable. Intended, and the thing to watch: the
  pool grows without anybody having opted into anything new.
- The values appear in ~40 files. Rewriting the fixtures is the real cost of this ADR.
- `saved_searches.filters` holding `availability` is refused as an unknown key, which tells
  the owner their saved question no longer means what it did.

## Open questions
