# RFC-0018 — The contributor profile

**Status:** Approved 2026-09-13 · **Binding form:** [ADR-0018](../adr/ADR-0018-contributor-profile.md)
· **Schema:** [`RFC-0018-*.schema`](RFC-0018-contributor-profile.schema)
· **Amends:** ADR-0010 §5 · **Source:** `flow-deisgn/contributor-page.png`

## Summary

A contributor signs in with GitHub and the platform knows what they have built. It knows
nothing about what they want.

This adds the second half: what shape of work they will take, where they are, how long
they have worked, and what they expect to be paid. It is what a role will be matched
against, so it is the prerequisite for roles rather than a screen bolted beside them.

The resume is **deliberately out of scope**, on the owner's instruction. It needs file
storage the platform does not have, and that is a decision of its own.

## 1. Two tables, not columns on `users`

`users` holds what GitHub supplies — display name, avatar, bio, location — and a sign-in
callback overwrites it. What this RFC adds is typed by the person, and must survive every
future sign-in untouched. Two lifetimes, two tables.

Compensation is split off again, into a third. It is the most sensitive thing a
contributor states, and the separation is the point: a query reading work preferences does
not incidentally read somebody's salary expectation. That is a property of the schema
rather than of whoever writes the next query.

## 2. Four flags, and freelance is not one of them

`availability_status` already exists and answers **am I looking, and how keenly**:
`not_looking`, `looking_for_job`, `looking_for_freelance`, `open_to_freelance`. It carries
an expiry, and that expiry is load-bearing — ADR-0010 §5 hides a lapsed contributor from
default search, which is what stops the pool filling with people who stopped looking a
year ago.

The `OpenTo*` flags answer a different question: **what shape of work**. Remote,
internship, onsite, contract. They are independent, because someone open to a remote
contract and an onsite permanent role is stating two things and an enum would make them
choose.

**There is no `open_to_freelance` flag.** The source diagram has one, and it is dropped on
the owner's instruction: the status already carries freelance twice over, and two controls
that both mean the same thing can disagree — leaving a UI to decide which wins, differently
in each place somebody reads it.

So the status keeps freelance and the flags cover what it does not: where the work happens,
and what kind of engagement it is.

**Availability applies to the shapes you enabled, and only those.** The two compose rather
than sitting side by side: declaring availability marks a contributor available _for every
`OpenTo_` they have turned on\*, and for nothing else. "Available" on its own is not a
state anybody is in — a person is available for remote contract work, or for an
internship, or for whichever shapes they ticked.

So a match needs both halves. The window must be live (ADR-0010 §5), **and** the flag for
that shape must be true. One refresh keeps every enabled shape current, which is what makes
a second expiry unnecessary: the flags do not lapse because availability lapsing already
takes all of them down together, and a standing preference — "I would consider remote
work" — does not go off the way "I am looking right now" does.

It also means **availability with no flags enabled matches nothing**. That is a real state
a person can put themselves in by accident: they refresh their window, believe they are
findable, and are invisible to every role because they never said what kind of work they
would take. The profile screen has to say so at the moment it happens rather than leaving
them to wonder why nobody writes.

**Defaults are false.** A profile nobody has filled in surfaces nobody. Opting a
contributor into internships by inaction would put them in front of hirers they never
agreed to hear from, and this platform's entire consent model is built the other way
(ADR-0005, ADR-0008 §3a).

The alternative was to strip the two freelance values out of `availability_status`
instead. It was not taken because that enum is load-bearing — `search` filters on it, the
seeded cast uses it, and two approved fixtures pin it — so the churn would be real and the
behavioural gain nil. Removing the newcomer costs nothing and resolves the same
duplication.

## 3. Years worked, and the work itself

`office_yoe` is years in a job, **self-reported and presented as such** — attributed to the
person, never asserted by the platform, exactly as a company's interview process is under
ADR-0017.

**The first and latest pull requests are ASKED, not derived.** An earlier draft read them
off a contributor's claims, and that was wrong: a first contribution is often years old, in
a repository they never claimed here, and a claim is the five PRs somebody chose as their
**best** — not their earliest and not their most recent. "Started contributing in 2024"
read off a claim is the wrong fact stated confidently, which is worse than not stating it.

`first_pr_url` is **write-once**. When somebody started is a fact about the past, and a
field revisable whenever it suited would be worth nothing to the hirer reading it. A second,
different value is refused with a readable error, and the column coalesces so the guarantee
does not rest on anybody remembering the check. `latest_pr_url` is editable, because it goes
stale by definition.

What a hirer gets from this is a link they can open, not a number somebody typed. "Here is
my first merged pull request" is checkable in one click; "twelve years of open source" is
not, and on a platform whose whole argument is evidence over assertion the difference is
the point.

Both halves matter and they are not the same: someone whose first contribution is from 2016
and who has been employed for one year is a specific person, and a hirer should be able to
see that.

## 4. Money is minor units, and needs a currency

`hourly_rate` and `yearly_amount` are `bigint` minor units — `45_00` is forty-five of
whatever `currency` says. Never a float: 0.1 + 0.2 is not 0.3, and money that does not add
up is money nobody trusts.

`currency` is ISO 4217 and required as soon as either amount is set. A number with no
currency is not a rate, and inferring one from `current_country` would be worse than
asking — people are paid in currencies they do not live in.

Both amounts are optional and independent: a freelancer may state an hourly rate and no
annual figure, a permanent candidate the reverse.

## 5. A hirer never sees what a contributor expects to be paid

Not on a scorecard, not on a contact request, not after an acceptance, not ever. There is
no endpoint and no field on any hirer-facing shape that carries it.

The reason is not squeamishness about salary. It is that **a hirer who knows what you will
accept can offer exactly that**, and no more. An expectation disclosed stops being a floor
and becomes a ceiling — the number anchors the offer, and the contributor who was honest
about it is the one who loses. A platform that collected the figure and handed it over
would be running an auction where only one side sees the bids.

So what is it FOR? Matching, in one direction only: **it filters what the CONTRIBUTOR
sees**. A role paying less than someone asked for is not shown to them. The hirer learns
nothing — not the number, not a range, not whether a given person was filtered out.

That direction matters more than it looks. Filtering the hirer's list by compensation would
leak the figure by inference: a hirer whose role pays £X, seeing who matched, learns that
everybody in the list will work for £X or less. An upper bound is not the number, but it is
most of the way there, and repeated across several roles it is the number. Matching only on
the contributor's side means nothing about their expectation reaches the hirer at all.

The cost is real and worth stating: a hirer cannot rule out a mismatch before approaching
somebody, so some approaches will be wasted on both sides. That is the price of the
contributor not being anchored, and it is the right way round — the person with less power
in the exchange is the one protected.

## 6. `current_country` is not `users.location`

`users.location` is free text GitHub supplies and a sign-in overwrites. `current_country`
is ISO 3166-1 alpha-2, typed by the person, and survives.

The reason it is a code rather than a name is the same one that made `organization_addresses.country`
one: a role's eligible countries are matched against it, and `"UK"`, `"U.K."` and
`"Britain"` cannot all be the same filter.

## 7. The country list comes from a third party. The rest of an address does not.

There are around 250 countries and the list changes; a hardcoded one is wrong the week
somebody secedes or renames. So the list is looked up, behind `port.PlaceService`,
alongside every other third party (ADR-0010):

```go
type PlaceService interface {
    // Countries backs every country picker on the platform.
    Countries(ctx context.Context) ([]Country, error)
}
```

One method. **Postcodes are NOT looked up**, on the owner's instruction, and the rest of an
address is free text as it already is.

That is a defensible line rather than a shortcut. A country code is a closed vocabulary
that two systems must agree on — it is matched against a role's eligible countries, so
`"UK"` and `"GB"` being different strings is a bug. A postcode is not matched against
anything; it is read by a person, posted to by a courier, and validating it buys accuracy
in a field nothing computes on. The asymmetry is real, and paying a network round trip for
the half that does not need it would be paying for nothing.

**Cross-cutting.** ADR-0017 already stores `organization_addresses.country`, validated by a
two-letter regex and nothing else, and adopts the same picker by amendment. Its
`postal_code` is untouched.

Two properties this has to keep.

**It fails open.** If the provider is down, a form with a country field still submits —
falling back to accepting any well-formed two-letter code. A signup path that depends on a
third party's uptime stops working on their bad day, and unlike an OAuth exchange nothing
here is a security decision.

**The stand-in serves it in development and in the suite.** `cmd/fakethirdparty` already
answers for GitHub, Google, Resend and the model; this is the fifth. No test and no
`make dev` reaches the live provider, for the reason ADR-0010 gives: the suite runs on a
laptop with no credentials.

The list is cached in memory for the process lifetime. It changes a few times a decade, and
a picker should not cost a network round trip.

Worth recording, because somebody will ask: a list this static could be embedded in the
binary instead, and that would have no provider, no uptime question and no adapter. A
lookup was chosen anyway, to keep a 250-row table out of the source and let the list be
corrected without a deploy.

## 8. Endpoints

| Method | Path               | Who             | Purpose                                 |
| ------ | ------------------ | --------------- | --------------------------------------- |
| `GET`  | `/me/profile`      | the contributor | read back everything below              |
| `PUT`  | `/me/profile`      | the contributor | work preferences, country, office years |
| `PUT`  | `/me/compensation` | the contributor | currency and expectations               |

`PUT` rather than `PATCH`, and two of them rather than one. A profile form submits every
field it shows, so a full replace says what happened; and compensation is separate at the
endpoint for the same reason it is separate in the schema — a client that only wants to
update availability preferences never sends a salary.

`/places/countries` is public and carries nothing about anybody. It is needed by the
organisation onboarding form, which has no account behind it either.

Nothing else here is public, and nothing here is readable by a hirer. `/me/compensation` is
the only way the figure is ever returned, to the person who typed it.

## Consequences

- The pool gains fields nothing filters on yet. Roles are what will read them, and until
  that RFC exists these are written and displayed but not matched.
- A contributor can be available and unmatchable: a live window with no `OpenTo*` enabled
  matches no role at all. The screen must say so when it happens.
- **Compensation is write-and-read-back-only until roles exist.** A contributor can state
  it and nothing consumes it. That is honest — the field is for a matching engine not yet
  built — but it should not be presented to them as doing something it is not.
- The roles RFC inherits a hard constraint: compensation filters the CONTRIBUTOR'S view of
  roles and must never filter, rank or annotate a hirer's view of people (§5).
- A fifth third party joins the boundary, and `cmd/fakethirdparty` grows a fifth
  responsibility. Nothing in the suite or in `make dev` reaches the live provider.
- ADR-0017 needs an amendment: its country field adopts the picker. Its `postal_code` is
  unchanged and stays free text.
- Three tables now hang off `users` — availability, work preferences, compensation — and a
  contributor's full picture is a join of four. That is the cost of keeping GitHub's data
  and the person's data apart, and of keeping salary out of incidental reads.

## Open questions

None. The one raised — which provider serves the country list — is settled for now by the
owner: **the stand-in serves it**, and no live vendor is chosen. `port.PlaceService` exists
so that choosing one later is an adapter and a flag rather than a change to anything above
it.
