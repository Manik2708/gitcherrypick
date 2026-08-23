# ADR-0010 — The third-party boundary, and how e2e avoids crossing it

**Status:** Accepted · **From:** [RFC-0010](../rfc/RFC-0010-third-party-boundary-and-e2e-adapters.md)
· **Schema:** _(no delta — this ADR changes no table)_
· **Amends:** none. Supersedes a design that existed only in `backend/e2e/control.go` comments.

## Decision

1. **Third-party adapters are redirected, never substituted.** Each of the four clients that
   talk to somebody else's server takes its base URL from a cobra flag defaulting to the real
   host. e2e passes the fake server's address. The same compiled client runs in both.

   | Flag                   | Default                       | Interface            |
   | ---------------------- | ----------------------------- | -------------------- |
   | `--github-api-url`     | `https://api.github.com`      | `port.GitHubClient`  |
   | `--github-oauth-url`   | `https://github.com`          | `port.GitHubClient`  |
   | `--google-oidc-issuer` | `https://accounts.google.com` | `port.OAuthProvider` |
   | `--anthropic-api-url`  | `https://api.anthropic.com`   | `port.AIClient`      |
   | `--resend-api-url`     | `https://api.resend.com`      | `port.Notifier`      |

2. **No build tags, and no fake implementation of any `port` interface in `internal/`.**
   There is one wiring path in `cmd/api`, and the binary the suite exercises is the binary
   that ships.

3. **The five adapters that are ours stay real in e2e** — `TokenIssuer`, `PasswordHasher`,
   `TokenMinter`, `Broker`, `Clock`. The suite runs real argon2id, signs and verifies real
   Ed25519 JWTs, and holds no credential shortcut.

4. **`cmd/fakethirdparty` holds no data.** It knows routes, status codes and response
   envelopes. Every value it returns comes from the fixture's `third_party` block, installed
   by `POST /_load` before each fixture.

5. **An undeclared route returns the provider's real 404**, never a default. A fixture that
   forgot to declare a PR fails as a missing PR rather than passing on an invention.

6. ~~**Time is controlled by seeding, not by intercepting `Now()`.** No fake clock exists.~~
   **RETRACTED by [ADR-0012](ADR-0012-controllable-clock-and-job-triggers.md).** Seeding
   reaches state that exists before a fixture runs, and not state the API creates during one —
   the seven-day claim lock has no row to seed. The clock is a redirected dependency.

## Implementation

### Why redirection rather than a fake adapter

What is under test decides it. A fake adapter leaves the real client's JSON decoding, error
mapping, pagination and retry handling with **zero** coverage until the first production
request — the most expensive place to find an adapter bug. Redirection executes those paths on
every e2e run.

### Why not a build tag

Two implementations behind `//go:build fakeadapters` gives a real guarantee — the fake package
is linked out of production — and pays for it three times. Each variant compiles only under
its own tag, so `go build`, `go vet` and `golangci-lint` must run twice or half the tree goes
unchecked. `gopls` resolves one variant and the other rots unnoticed. And the tested binary
stops being the shipped binary, which is what an integration suite exists to prevent.

Redirection needs none of it: production cannot link out fake code that was never written.

### The residual risk, accepted

A base URL is configuration, so a misconfigured deployment can point at the wrong host. This
is the same class of risk as the database URL and carries the same mitigation — default to the
real value, treat the flag as sensitive.

### Time — partly retracted

The availability window is 30 days and a refresh token is 30 days, so a fixture seeding
`last_seen_at` 31 days back reads as lapsed against the real clock. That reasoning stands, and
it covers most fixtures.

What it does NOT cover, and what this section wrongly claimed it did, is state the API creates
during a fixture: the seven-day claim lock, the escalating re-evaluation cooldown, invitation
expiry. ADR-0012 supersedes this with a clock whose source is configurable.

`--access-token-ttl` still covers a 15-minute access token expiring mid-run.

## Steps

1. Implement the four clients in `internal/adapter/{github,google,anthropic,resend}`, each
   taking `BaseURL` in its config struct.
2. Build `backend/cmd/fakethirdparty`: provider-shaped routes, `POST /_load`, `GET /_sent/...`.
3. Extend `fixture.schema.json` with `third_party`, and teach `validate_fixtures.py` to reject
   a fixture whose steps reference an undeclared PR, repository or OAuth code.
4. Rewrite `e2e/control.go` against `/_load` and `/_sent`; delete `ApplyFakes` and
   `RunControlAction` in their current form.
5. Extend `e2e.sh` to start and tear down the fake server alongside Postgres.
6. Wire the flags in `cmd/api`.

## Consequences

- One binary, one build, one lint pass.
- Real adapter HTTP handling gains coverage it would otherwise never have.
- **The fake server must track provider wire formats.** A provider changing a response shape
  breaks production while e2e stays green. This is the genuine price of the approach.
  Contract tests against recorded live responses are the mitigation and are out of scope here.
- `cmd/fakethirdparty` is a second binary, and test infrastructure that ships in the repo.
- Expected third-party values live in the fixture beside the assertions they drive, rather
  than in a server that would otherwise become a second source of truth about test data.

## Amendments

- **ADR-0012** retracts Decision 6 and rewrites §5. Time cannot be controlled by seeding alone;
  the clock is a redirected dependency, like the base URLs. `port.ControlPlane` is deleted
  there too — this ADR removed the `--adapters=fake` mechanism behind it without replacing the
  capability, which ADR-0012 supplies outside the API process.
