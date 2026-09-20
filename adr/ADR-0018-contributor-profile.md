# ADR-0018 — The contributor profile

**Status:** Accepted · **From:** [RFC-0018](../rfc/RFC-0018-contributor-profile.md)
· **Schema:** [`RFC-0018-*.schema`](../rfc/RFC-0018-contributor-profile.schema)
· **Amends:** ADR-0010 §5, ADR-0017 §3 · **Source:** `flow-deisgn/contributor-page.png`

## Decision

1. **A contributor states what work they will take, where they are, how long they have
   worked, and what they expect to be paid.** The platform already knows what they built;
   this is the half it does not know.
2. **A hirer NEVER sees expected compensation.** Not on a scorecard, not on a contact
   request, not after an acceptance, not ever. There is no endpoint and no hirer-facing
   field that carries it.
3. **Compensation filters the CONTRIBUTOR'S view of roles, never the hirer's view of
   people.** Filtering a hirer's results would leak the figure by inference — an upper
   bound across a few roles is the number.
4. **Availability and the `OpenTo*` flags compose.** Declaring availability marks a
   contributor available for every flag they have enabled and for nothing else. A match
   needs a live window AND the flag for that shape.
5. **The flags do not lapse separately.** Availability lapsing takes all of them down
   together, so one clock governs, not two.
6. **There is no `open_to_freelance` flag.** `availability_status` already carries
   freelance, and two controls meaning one thing can disagree.
7. **The first and latest pull requests are ASKED, not derived.** A first contribution is
   often years old in a repository nobody claimed here, and a claim is five PRs chosen as
   somebody's **best** — not their earliest. `first_pr_url` is **write-once**;
   `latest_pr_url` is editable. `office_yoe` is self-reported and presented as such.
8. **Money is `bigint` minor units with a required ISO 4217 currency.** Never a float.
9. **The country list comes from `port.PlaceService`; postcodes do not.** A country code is
   a closed vocabulary two systems must agree on; a postcode is matched against nothing.
10. **The stand-in serves the country list**, and no live vendor is chosen. Choosing one
    later is an adapter and a flag.
11. **Three tables, not columns on `users`.** `users` holds what GitHub supplies and a
    sign-in overwrites; this is typed by the person and survives. Compensation is separate
    again so a read of work preferences never incidentally reads a salary.
12. **Defaults are false.** A profile nobody filled in surfaces nobody.

## Implementation

### Endpoints

| Method | Path                | Who             | Purpose                                 |
| ------ | ------------------- | --------------- | --------------------------------------- |
| `GET`  | `/me/profile`       | the contributor | work preferences, country, office years |
| `PUT`  | `/me/profile`       | the contributor | replace them                            |
| `GET`  | `/me/compensation`  | the contributor | their own expectations                  |
| `PUT`  | `/me/compensation`  | the contributor | replace them                            |
| `GET`  | `/places/countries` | anyone          | the country picker                      |
| `GET`  | `/money/currencies` | anyone          | the currency picker                     |

`PUT` and not `PATCH`: a profile form submits every field it shows, so a full replace says
what happened. Two pairs and not one, so a client updating work preferences never sends a
salary.

`/places/countries` is public because the organisation onboarding form needs it and has no
account behind it either (ADR-0017 §2).

### The unmatchable state

A live availability window with no `OpenTo*` enabled matches **nothing**. This is reachable
by accident — refresh the window, believe you are findable, be invisible to every role —
and it is undiagnosable from outside. `GET /me/profile` reports it as a named field rather
than leaving a client to derive it, and the screen says so at the moment it happens.

### Compensation

`GET /me/compensation` is the only read that ever returns the figure, to the person who
typed it. Its absence from every other shape is the decision, so it is asserted by a test
rather than left to review.

### PlaceService

```go
type PlaceService interface {
    Countries(ctx context.Context) ([]Country, error)
}
```

Behind ADR-0010's boundary, served by `cmd/fakethirdparty`. It **fails open**: a provider
that is down falls back to accepting any well-formed alpha-2 code, because nothing here is
a security decision and a signup path should not depend on a third party's uptime. Cached
in memory for the process lifetime.

## Steps

1. **Schema.** `user_work_preferences` and `user_compensation` are the RFC-0018 delta. No
   edits at origin — `users` is deliberately untouched.
2. **Ports.** `ProfileRepository`, `ProfileService`, `PlaceService`.
3. **Repository.** Upsert and read for both tables, with `first_pr_url` coalesced so a
   second value cannot overwrite the first.
4. **Service.** Validation, the unmatchable-state report, and the composition rule.
5. **Controllers.** The five endpoints. `/places` is a new public controller.
6. **Adapter + stand-in.** `PlaceService` over HTTP, and the country route in
   `cmd/fakethirdparty`.
7. **Frontend.** The profile screen, on the contributor's "Being found" surface.
8. **Fixtures.** Written by the integration-tester. The assertion that matters most is
   decision 2: compensation appears in no hirer-facing response.

## Consequences

- Compensation is write-and-read-back-only until roles exist. Nothing consumes it, and a
  contributor should not be told otherwise.
- The roles RFC inherits a hard constraint from decision 3.
- A contributor's full picture is a join of four tables.
- ADR-0017's country field adopts the picker; its `postal_code` is unchanged.
- A fifth third party joins the boundary, and `cmd/fakethirdparty` grows a fifth
  responsibility.

## Amendments

### 1. The first and latest pull requests are asked, not derived

Decision 7 originally derived an open-source-years figure from the earliest merged PR in a
contributor's claims. Corrected by the owner during implementation, and the correction is
right: **a contributor's first pull request may not be in any claim at all.**

A claim is five PRs somebody chose as their best work. It is not their earliest and not
their most recent, and a first contribution is often years old in a repository they never
thought to claim here. Deriving "started contributing in 2024" from that data would be
stating the wrong fact confidently — which on a platform built to replace assertion with
evidence is worse than not stating it.

So both are asked. `first_pr_url` is write-once, because when somebody started is a fact
about the past and a field they could revise downward whenever it suited would be worth
nothing to a hirer. `latest_pr_url` is editable, because it goes stale by definition.

The write-once rule is enforced twice: the service refuses a second, different value with
`first_pr_is_fixed` so a client is told rather than silently overruled, and the repository
coalesces so the guarantee survives a caller who forgets to check.

### 2. A contributor who never fills the form in is prompted

Nothing in the original steps asked. A contributor signed in with GitHub, landed on their
standing, and "Being found" was a nav item they had no reason to click — so the common case
was somebody invisible to every role with no way to suspect it.

`GET /me/profile` now reports `needs_attention`, true only when the person has **never**
stated anything. It is deliberately not the same as `matchable: false`, which is a
legitimate state for somebody who is not looking, and not the same as every flag being
false, which is a real answer. Nobody should be nagged about a decision they made.

### 3. The currency list comes from the third party too

Decision 9 put COUNTRIES behind `port.PlaceService` because a country code is a closed
vocabulary two systems must agree on. Decision 8 requires an ISO 4217 currency on every
amount — and then left it as a text box on two forms.

The argument for the country picker applies unchanged. An amount is only ever compared
against another amount **in the same code**: a role's salary against a contributor's
expectation, in search and in the public-opening gate, both of which skip the comparison
when the codes differ. So a hirer who typed `usd`, `US$` or `dollars` wrote a salary that
is compared against nothing and was told nothing about it, because the shape check passes
and the match silently does not. That is the failure mode a picker removes at the point it
happens, rather than a validator refusing a form afterwards.

`port.MoneyService` is a **separate interface** from `PlaceService`, not a second method on
it: a currency is not a place, and a live vendor for one is not a live vendor for the
other. They are served by the same stand-in today and configured by two flags
(`--places-api-url`, `--money-api-url`), so choosing a country provider later is not
accidentally a decision about salaries.

`GET /money/currencies` is public and **fails open** exactly as the country picker does:
200 with an empty list and `degraded: true` when the provider is down, the service still
validating the shape of a code and not its membership. A hirer writing a role at 2am does
not care whose uptime the picker depends on.
