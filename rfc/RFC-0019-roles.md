# RFC-0019 — Roles

**Status:** Approved 2026-09-16 · **Binding form:** [ADR-0019](../adr/ADR-0019-roles.md)
· **Schema:** [`RFC-0019-*.schema`](RFC-0019-roles.schema)
· **Depends on:** [ADR-0005](../adr/ADR-0005-scoring-and-discovery.md),
[ADR-0008](../adr/ADR-0008-discovery-http-surface.md),
[ADR-0017](../adr/ADR-0017-organisation-onboarding.md),
[ADR-0018](../adr/ADR-0018-contributor-profile.md)
· **Source:** `flow-deisgn/hirer-flow.png`

## Summary

The platform can rank contributors, and a contributor can now say what work they want. What
nobody can state is **the job** — there is no row anywhere that says what a company is
hiring for, what it pays, where it is, or who it can legally employ.

This is the third piece of the hirer flow, and the last one missing. It is also the piece
the other two were built for: ADR-0018 §2 says a contributor's `OpenTo*` flags are "what a
role will be matched against", and ADR-0017 deferred its three process fields to "the roles
RFC". Both of those promissory notes come due here.

A role is a **description of an opening**, not a pipeline. It holds what a contributor
needs in order to decide whether to talk, and stops there. Interviews, offers, and
rejections are somebody else's system.

## 1. Where a role sits in the flow

From the source diagram, a hirer about to shortlist asks `RoleExist?`. If not, they create
one; if so, they reuse it. Either way the shortlist that follows is **for a role**.

**Decided: one shortlist, one role.** `shortlists.role_id`, `NOT NULL`. A hirer who wants
the same contributor for a second opening opens a second shortlist. The contact request
reads its role by following the shortlist it belongs to.

That ordering is the whole design. A shortlist today (RFC-0005) is a named list with a
`tentative_result_date` and nothing else — a hirer can open "Backend hiring, Q4" and the
contributor who is contacted learns nothing about what the job is. Attaching a role to the
shortlist is what turns the contact request from "someone is interested in you" into
"someone is interested in you, for this, at this salary, in these countries".

One role per shortlist rather than a role per _entry_ because a shortlist is a **round of
hiring**, and a round has one answer to "what is this for". The overdue ratio in ADR-0005
§10 already assumes that: it measures whether an organisation resolves the rounds it opens,
which means nothing if a single round is four jobs at once with four different timelines.
Per-entry roles would also push the choice to confirm time, person by person, which is the
moment ADR-0008 made irreversible and the worst moment to be deciding what somebody is
being approached _for_.

The duplicate this creates is contained. `uq_contact_request UNIQUE (shortlist_id, user_id)`
is per shortlist, so a second shortlist for a second role raises a second, separate request
against the same contributor — which is the honest representation: two jobs, two decisions,
two answers. The contributor is told twice because they are being asked two different
questions.

This is a one-column edit to **RFC-0005 at origin**, made when the ADR is implemented.
Nothing is deployed, so no `ALTER` — same precedent as ADR-0016 step 1 and ADR-0017 step 1.

## 2. The email is the contact request, not a way around it

**Decided.** The diagram shows `Shortlist → MoreYesNoQuestionsForContributor →
SendEmailWithRoleDetails` with no acceptance step drawn between them, which reads as a
direct approach. Taken literally that would undo ADR-0005 §9 — shortlisting discloses no
email; a contributor accepts first, and `email_released_at` is stamped only then.

It is not a direct approach. **`SendEmailWithRoleDetails` _is_ the contact-request
notification, carrying more.** Everything the contributor needs in order to answer — the
role, what it pays, where it is, who it can employ, what the process looks like, and who
the organisation is — is attached to the request and sent **by the platform**. The
contributor's address is disclosed to nobody. Acceptance is what releases it, exactly as
`email_released_at` already records.

So the invitation stops being anonymous without the consent model moving an inch. That is
the better trade in both directions: a contributor can decline informed rather than
declining on suspicion, and a company gets fewer yeses that evaporate on the first call.

The consequence for this RFC is that a role is **never a channel**. It is a body of
information that a contact request carries. No endpoint here sends mail, and no column here
holds an address.

## 3. One table, one discriminator

A freelance role and a permanent one differ in exactly two columns — an hourly rate and
expected hours, against a yearly package and base. Everything else is identical: title,
description, location, eligible countries, experience minimums, process. Two tables would
duplicate fifteen columns to avoid two nulls, and every query that lists "our open roles"
would become a union.

So: one `roles` table, `engagement` as the discriminator, and a `CHECK` that a freelance
role carries no yearly figure and a salaried one carries no hourly rate. The constraint is
what makes the nulls safe — a role advertising both is a role that cannot be compared
against anybody's expectation, and the database refuses to hold one.

`engagement` is an **enum**, unlike a contributor's `OpenTo*` flags, which are independent
booleans. The asymmetry is deliberate and correct: a person will consider several kinds of
work at once; an opening is one kind of work.

## 4. Location reuses an address, and remote means remote

`location` is `remote` or `address`. When it is `address`, `address_id` points at a row in
`organization_addresses` — the table ADR-0017 created, which already anticipated this: its
`country` column is `char(2)` precisely so "a role that filters on eligible countries later
needs one value per country".

The diagram's `AddressExist?` branch is that reuse. A second role at the same office points
at the same row rather than retyping it, so correcting an address corrects every role at it.
`ON DELETE RESTRICT`, because deleting an address out from under an open role would leave a
posting that claims to be somewhere and is not.

A `CHECK` ties the two together in both directions: an address role must have an address,
and a remote role must not carry a stale pointer to the office it used to be at.

## 5. Eligible countries: a child table, and empty means anywhere

`role_eligible_countries` is a join table rather than an array column, so matching a
contributor's `current_country` (ADR-0018 §6, also alpha-2) is an indexed join rather than a
scan of every open role.

**No rows means no restriction.** A role that enumerated all 249 countries would be saying
the same thing at far greater length, and — more importantly — an organisation that never
thought about work authorisation should not silently exclude the world by leaving a field
blank. The permissive reading is the one that fails towards a contributor being able to see
the role, which is the direction this platform should fail in.

## 6. Experience minimums are minimums, and `NULL` is not zero

`min_office_yoe` and `min_oss_yoe`. A `NULL` means no minimum; a `0` means the role has
explicitly decided it will take someone with no commercial experience. Those are different
invitations and a new graduate reads them differently, so the schema keeps them apart.

`min_office_yoe` compares against `user_work_preferences.office_yoe`, which the contributor
states and ADR-0018 §3 already frames as "something you said, not something we checked".

### Open-source years are verified, and dated from authorship

**Decided.** `min_oss_yoe` compares against a figure the platform **derives and checks**,
not one anybody types. The contributor gives the pull request; we establish the date.

The date is when the pull request was **authored**, not when it was merged. Those come apart
badly: a first contribution can sit in review for eight months, or two years, or be merged
by a maintainer long after the person moved on. Merge date measures a project's
responsiveness. Authored date measures when somebody started contributing, which is the
thing being asked about.

This makes open-source years the **one number on a contributor's profile that is evidence
rather than testimony** — which is the platform's entire argument, applied to the field
where it was easiest not to bother.

Three additions to `user_work_preferences`, made at origin in **RFC-0018**:

| column                  | what it holds                                                  |
| ----------------------- | -------------------------------------------------------------- |
| `first_pr_authored_at`  | when the first pull request was opened — the start of the span |
| `latest_pr_authored_at` | the same for the latest one, fetched in the same pass          |
| `first_pr_verified_at`  | when we last confirmed the first PR, or `NULL`                 |

Open-source years is `now − first_pr_authored_at`, on `r.db.now()` and not SQL `now()`
(ADR-0012).

**Authorship is checked, not assumed.** `PRFacts.AuthorUserID` must equal the contributor's
GitHub user id. Without that check, "years contributing" is a field anybody can fill by
pasting a stranger's 2011 pull request — and the write-once rule would then make the lie
permanent. This is the reason the check has to happen _before_ the URL is stored rather than
afterwards.

`PRFacts` has `MergedAt` but no authored date. Adding `CreatedAt` to it, and to the GitHub
adapter and its stand-in, is part of the work.

### What happens when the fetch fails

A save that we can **answer** — the PR does not exist, is not merged, or was written by
somebody else — is rejected `422` and **nothing is written**. Write-once stays honest
because an unverified URL never becomes the permanent one.

A save we **cannot** answer — rate limit, GitHub down — stores the URL with
`first_pr_verified_at` left `NULL`, and a job retries. Until it verifies, the contributor's
open-source years are _unknown_, and unknown does not match a role that sets a minimum. That
is a fail-closed, against the fail-open this corpus otherwise prefers (ADR-0018 §7), and it
is deliberate: failing open here would mean a minimum that anybody clears by having an
unverifiable link, which is worse than a filter that is briefly strict. The contributor is
told on their own profile that verification is pending, so the state is visible to the one
person who can do anything about it.

## 7. The process fields land here

ADR-0017 deferred `requires_online_test`, `max_interview_rounds` and `avg_days_to_offer`
with two properties already decided: shown on the **contact request and nowhere else**, and
visible only to **the contacted contributor and the organisation that contacted them**.

They go on the **role**, not the organisation. A staff engineering role and a three-month
contract at the same company do not share an interview process, and hanging these off
`organizations` would state one answer where there are several — then show it to everyone.

All three are nullable. A company that has not measured its average time to offer should
leave it blank rather than invent a number that a contributor will later hold it to.

## 8. Screening questions are yes/no, and they do not gate

`role_questions` is the diagram's `MoreYesNoQuestionsForContributor` — work authorisation,
relocation, notice period, the things a company genuinely needs before a first call.

**Closed questions only.** A free-text question is an interview, and an interview conducted
before either side has agreed to talk is exactly the asymmetry the consent model exists to
prevent. A yes/no is answerable in one tap and discloses only what it asks.

`expected` records the answer the organisation is hoping for, so an answer can be _reported_
as matching or not. It does **not** filter: a required answer would be a hidden rejection
the contributor never saw, from a question they were never shown until they were already
being approached. The company reads the answers and decides; the platform does not decide
for them.

Answers are keyed to the **contact request**, not to the contributor. That is what they
consented to — one approach, by one organisation — and it means an answer about visa status
does not follow somebody around the platform.

## 9. Compensation points the same way it did in ADR-0018

Minor units and an ISO 4217 currency, because the two numbers are compared and must be the
same kind of number. `yearly_ctc` and `yearly_base` both, because "total package" and "what
arrives monthly" are the distinction a contributor most needs and most often is not given.

ADR-0018 §5 is unchanged and untouched: a contributor's expectation filters **their** view
of roles, and is never shown to a hirer. This RFC adds the other side of that comparison and
nothing else. The direction of visibility stays one-way.

## 10. Status, and who may write

`draft → open → closed`. A draft matches nobody; the partial index on `status = 'open'` is
the matching read. Closing is a state change rather than a delete, because a closed role is
still the thing a live contact request refers to.

### Authority is a setting, not a rule — one per action

A role is the first thing a hirer can create that makes a **financial statement on the
company's behalf**. A salary, a base, a number of interview rounds, an average time to
offer — sent in writing to everybody contacted and held to afterwards. A mistyped
`yearly_ctc` is not a mistake somebody notices internally; it is a promise already
delivered.

The existing precedent splits — the roster is owners-only (`requireOwnerOf`: granting a seat
grants the power to grant further seats), shortlists are open to any hirer — and a role sits
between them. **Decided: the owner chooses**, because which answer is right is a fact about
the company rather than about the platform. Three recruiters and a founder who is rarely
around want a different rule from two people who do everything together, and picking one for
them would be wrong for most of them.

**And they choose three times.** Creating, changing and closing are different sizes of
decision, and a single dial would force a company that wants an owner on new salaries to put
one on withdrawing a filled role as well. Three columns on `organization_settings`, each
taking the same three values:

| action                 | setting                 | default             |
| ---------------------- | ----------------------- | ------------------- |
| create and open a role | `role_create_authority` | `draft_and_approve` |
| change an open role    | `role_update_authority` | `draft_and_approve` |
| close an open role     | `role_close_authority`  | `any_hirer`         |

And the three values, read the same way whichever action they govern:

| value               | meaning                                    |
| ------------------- | ------------------------------------------ |
| `any_hirer`         | anybody with a seat does it outright       |
| `draft_and_approve` | anybody stages it; an **owner** commits it |
| `owners_only`       | only owners may do it at all               |

For **creation**, `draft_and_approve` is the `draft → open` split already in the schema: any
hirer writes the role, an owner opens it. That is the default because the work stays with
whoever is doing it — the diagram has the person shortlisting hit `RoleExist?` and create a
role mid-task, which is a recruiter, not an owner doing an administrative round — and the
commitment is still signed by somebody accountable for it.

For **closing**, `draft_and_approve` stamps `close_requested_by` and waits for an owner. The
role **stays open** meanwhile: it matches, it can be shortlisted against, and the promise it
makes stands until somebody actually withdraws it. A request is not an outcome and the
schema does not let it look like one. The default here is `any_hirer`, because closing only
ever withdraws a commitment, and a rule that makes the safe direction harder than the unsafe
one gets worked around.

For **changing an open role**, there is no change to authorise in place — §11 makes an open
role immutable, so an edit is a new role, and `role_update_authority` governs who may write
that revision and who may open it.

Only an owner may change the settings themselves, under every combination. Otherwise a
member sets `any_hirer` and grants themselves the authority the setting exists to withhold —
the same reasoning that makes roster changes owners-only.

### The signature is not optional

`opened_by` and `opened_at` are stamped under **every** policy, including `any_hirer`, and
so is `close_requested_by`. _Who may_ act is configurable; _that whoever did is named_ is
not. A `CHECK` ties
them to the status, so a draft cannot carry a signature and an open role cannot lack one —
because "who approved this salary" is asked after something has already gone wrong, when
the service that was meant to set the column is not around to be asked.

### A role does not expire

**Decided: no expiry, and no target date.** A role is open until somebody closes it, and
closing is the action the settings above govern.

The alternative was symmetry with what already exists — availability lapses (ADR-0010 §5) so
the pool does not fill with people who stopped looking, and a shortlist carries a
`tentative_result_date` that gets flagged when an organisation misses most of them
(ADR-0005 §10). Neither is extended here.

The asymmetry that leaves is real and accepted: a contributor must keep saying they are
still looking, and a company need not keep saying it is still hiring. A role whose headcount
was quietly cut can stay open, keep appearing in `/me/roles`, and keep collecting shortlists.
If that starts happening, the fix is the flagging mechanism ADR-0005 already computes rather
than an expiry — nothing here forecloses it.

An open role is never edited at all — §11.

## 11. An open role is immutable

**Decided.** A role that has been opened is never edited. Changing one writes a **new role**
carrying the new details, with `supersedes_id` pointing at the old one, and closes the old
one in the same transaction.

The reason is the one the question was really about: the salary somebody was shown is the
salary they agreed to talk about. Every contact request reaches its role through
`shortlists.role_id`, so a request raised last week keeps pointing at the row it was raised
against. A contributor who accepted "£95,000, remote, three rounds" can still see exactly
that, months later, whatever the company is advertising now. Guarding an `UPDATE` would have
got this right only for as long as everybody remembered the guard; a row nobody updates gets
it right permanently.

It is the same device as `organization_onboarding.supersedes_id` (ADR-0017), for the same
reason: the superseded record is evidence of what was said, and evidence that can be edited
is not evidence.

**Drafts are mutable.** The line is disclosure, not age — a draft has been shown to nobody,
and turning every keystroke into a row would be absurd. `PATCH` edits a draft; `revise`
drafts a successor to an open role. A unique partial index on `supersedes_id` keeps it a
chain rather than a tree, because two revisions of one role would leave two rows each
claiming to be the current version and nothing able to say which a contributor should see.

Closing the superseded role is part of the revision and does **not** consult
`role_close_authority` — it is one act, authorised once, under `role_update_authority`.
Requiring a second approval to retire a row that has just been replaced would leave two open
roles for one job while somebody went looking for an owner.

The cost is that "our roles" is now a list that needs filtering: a company with a role
revised four times has five rows and one live opening. `WHERE supersedes_id IS NULL` is the
wrong filter — it is the newest row, not the oldest, that is live. The list reads
`status = 'open'`, which the partial index already serves.

## 12. Closing a role asks why, and one answer names a person

A role does not just stop. Closing asks **why**, from four answers:

| reason               | what it means                                            | also required |
| -------------------- | -------------------------------------------------------- | ------------- |
| `not_needed`         | the headcount went away                                  | —             |
| `hired_elsewhere`    | filled, by somebody found some other way                 | —             |
| `hired_via_platform` | filled, by **one or more** people contacted through here | their emails  |
| `other`              | something else                                           | a note        |

A fifth value, `superseded`, is written by the system when a revision replaces a role
(§11). It is not offered to anybody, and it exists so that "every closed role says why"
holds without exception rather than nearly.

### Why this is the most valuable field in the RFC

`hired_via_platform` is **the only outcome signal the platform has ever had.** Everything
else here measures activity: claims submitted, PRs judged, contributors ranked, requests
accepted. None of it says whether any of it worked. A closed loop from _this person was
ranked_ through _this company contacted them_ to _they were hired_ is the evidence that the
ranking is worth anything — and it is the one measurement that cannot be reconstructed
later, because the only moment anybody knows the answer is the moment they close the role.

`hired_elsewhere` matters for the same reason and is not a consolation answer. A company
that keeps filling roles from elsewhere after shortlisting here is telling us something
specific, and telling us honestly only if the answer is as easy to give as the flattering
one.

### A role may fill several seats

Hires are a **table**, not a column. "Two backend engineers" is one posting and two people,
and a single `hired_user_id` would have recorded the first and quietly lost the rest — in
the one place this platform collects outcomes, which is the worst place to lose data.

So `role_hires` takes one row per person, and `hired_via_platform` means **at least one**
of the seats was filled from here. A role that hired two people from the platform and one
from an agency closes as `hired_via_platform` with two rows; the reason describes what the
platform did, and the rows say exactly who.

The number of seats a role has is deliberately not modelled. A company that wants to state
it can say so in the description, and inventing a `headcount` column would create a second
number that can disagree with the hires actually recorded against it.

The invariant — that reason has at least one hire, and no other reason has any — spans two
tables, so it cannot be a `CHECK`. It lives in the service and is asserted there, written in
the same transaction as the close.

### The emails must already have been released

The hirer types emails; the service resolves each to a contributor and stores ids, never
addresses. **Every address must be one an accepted contact request already released to this
organisation.**

Two reasons, and both are load-bearing. A company can only report hiring somebody who
agreed to talk to them — a hire it claims from an address nobody released did not come from
this platform, whatever the closing hirer believes. And without the rule, this field is an
oracle: type any address, learn from the error whether it has an account here. Restricting
it to addresses the organisation was _already given_ makes the probe worthless, because
they already know the answer for every address it accepts.

An unrecognised address is a `422` naming **which** address failed — the hirer may be
entering several and must not have to guess which one — and offering `hired_elsewhere`,
rather than a silent downgrade — a company that believes it hired through the platform and
is told nothing will believe it recorded something it did not.

### The contributor is told

Being recorded as hired is a claim about a person, and one they can see is wrong. The
contributor is notified: this organisation reported hiring you for this role. It is not
shown on their profile, not searchable, and not visible to any other organisation — the
same boundary the process fields sit behind (§7). If it is wrong, correcting it is a manual
job, which is proportionate to how often a company is expected to invent a hire.

**Availability is not touched.** It would be easy to flip somebody to `not_looking` on the
strength of this, and it is not the platform's call to make on the word of the other party
to the deal.

## Endpoints

| Method | Path                              | Who                                                             |
| ------ | --------------------------------- | --------------------------------------------------------------- |
| POST   | `/orgs/{orgID}/roles`             | per `role_create_authority` — creates a draft                   |
| GET    | `/orgs/{orgID}/roles`             | any org hirer                                                   |
| GET    | `/orgs/{orgID}/roles/{id}`        | any org hirer                                                   |
| PATCH  | `/orgs/{orgID}/roles/{id}`        | per `role_update_authority` — **drafts only**                   |
| POST   | `/orgs/{orgID}/roles/{id}/revise` | per `role_update_authority` — drafts a successor                |
| POST   | `/orgs/{orgID}/roles/{id}/open`   | per `role_create_authority` — signs the numbers                 |
| POST   | `/orgs/{orgID}/roles/{id}/close`  | per `role_close_authority`                                      |
| GET    | `/orgs/{orgID}/settings`          | any org hirer — so a member can see the rule they are under     |
| PUT    | `/orgs/{orgID}/settings`          | **owner only**, always                                          |
| GET    | `/me/roles`                       | contributor — roles they may see, filtered by their own profile |

`/me/roles` is the contributor-facing half: open roles whose eligible countries include
theirs, whose minimums they meet, whose engagement matches a shape they ticked, and whose
compensation is not below what they asked for — the filter ADR-0018 §5 promised, applied
where only they can see it.

## Consequences

- A shortlist stops being a bare list and becomes a round of hiring for a stated job. The
  contact request gets substantially more informative, which should raise acceptance rates
  and — more usefully — raise informed _declines_.
- One edit at origin: `shortlists.role_id NOT NULL` in RFC-0005. Every shortlist now names
  a job, including the ones the e2e fixtures seed. Nothing is deployed, so no `ALTER` (same
  precedent as ADR-0016 step 1 and ADR-0017 step 1).
- A contributor suited to two openings at one company receives two contact requests. That is
  intended — two jobs are two decisions — but it is also the most likely source of "why am I
  getting mail twice from these people", and the notification copy needs to name the role
  prominently enough that the second one does not read as a duplicate.
- An independent hirer still cannot create a role: `roles.organization_id` is `NOT NULL`.
  This is the open Planner item about independent hirers being unable to do anything
  org-scoped, and this RFC makes it more visible rather than solving it.
- Three columns and one adapter field added at origin: `first_pr_authored_at`,
  `latest_pr_authored_at` and `first_pr_verified_at` on `user_work_preferences` in RFC-0018,
  and `CreatedAt` on `domain.PRFacts` with the GitHub adapter and `cmd/fakethirdparty`
  following. Saving a profile stops being a pure write and starts making a network call,
  which is new for that endpoint.
- The platform gets its first outcome data. `role_hires` joined to the ranking is what
  answers "does this produce hires", and until roles ship there is no way to ask.
- A hire recorded against a contributor is an assertion by one party about another. It is
  notified and kept private, and it has no correction flow beyond a human.
- Role rows accumulate: one per revision, not one per opening. They are small, and the
  partial index on `status = 'open'` means the dead ones cost nothing to read past.
- Matching becomes a real query with several predicates. The partial index on open roles is
  sized for tens of thousands of roles, not millions; beyond that it wants revisiting.

## Open questions

None. All six were resolved with the owner on 2026-09-16, in the order recorded above:
consent (§2), one shortlist per role (§1), verified open-source years dated from authorship
(§6), per-action authority settings (§10), no expiry (§10), and immutability (§11).
