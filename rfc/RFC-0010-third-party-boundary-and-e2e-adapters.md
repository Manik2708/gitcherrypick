# RFC-0010 — The third-party boundary, and how e2e avoids crossing it

**Status:** Approved 2026-08-23 · **Binding form:**
[ADR-0010](../adr/ADR-0010-third-party-boundary-and-e2e-adapters.md)
· **Schema:** _(no delta — this RFC changes no table)_
· **Amends:** none. Supersedes an undocumented design that only ever existed in
`backend/e2e/control.go` comments.

## Summary

The e2e suite must not contact github.com, Google, Anthropic or Resend. Stage 3 assumed a
mechanism for that and never specified one: `e2e/control.go` describes an API binary started
with `--adapters=fake --control-addr=127.0.0.1:8081`, exposing a second listener that primes
in-memory fakes. No such flag, listener or fake exists, and `ApplyFakes` and
`RunControlAction` are called from nowhere.

This RFC replaces that design with one that ships **no fake code at all**.

## The boundary is narrower than it looks

Nine adapters sit behind `port`. Only four of them are somebody else's server:

| Adapter          | Backed by         | In e2e            |
| ---------------- | ----------------- | ----------------- |
| `GitHubClient`   | github.com        | redirected        |
| `OAuthProvider`  | Google OIDC       | redirected        |
| `AIClient`       | Anthropic Batch   | redirected        |
| `Notifier`       | Resend            | redirected        |
| `TokenIssuer`    | our Ed25519       | **real**          |
| `PasswordHasher` | our argon2id      | **real**          |
| `TokenMinter`    | our `crypto/rand` | **real**          |
| `Broker`         | our Postgres      | **real**          |
| `Clock`          | the host          | **real** (see §5) |

The five on the bottom are ours. Nothing forces them to be faked, and faking them would be
actively harmful: a fake `TokenIssuer` is an authentication bypass, and a fake
`PasswordHasher` means no test ever proves a password can be verified. **The suite runs real
argon2id, signs real Ed25519 JWTs, and verifies them through real middleware.** There is no
credential shortcut anywhere in it.

## 1. Redirection, not substitution

For the four that remain, the adapter is not replaced. Its **base URL** is configuration:

```
--github-api-url     default https://api.github.com
--github-oauth-url   default https://github.com
--google-oidc-issuer default https://accounts.google.com
--anthropic-api-url  default https://api.anthropic.com
--resend-api-url     default https://api.resend.com
```

e2e passes `http://127.0.0.1:8081/...` for each. Production passes nothing and gets the real
hosts. This is cobra config like every other setting (CLAUDE.md), not a test-only pathway.

The decisive property is what is under test. With a fake adapter, the real client's JSON
decoding, error mapping, pagination and retry handling have **zero** coverage until the first
production request — which is the most expensive place to discover an adapter bug. Under
redirection those paths execute on every e2e run.

## 2. Why not a build tag

The rejected alternative was two implementations selected by `//go:build fakeadapters`.

It gives a real guarantee — the fake package is linked out of production entirely — but pays
for it three times. Every variant is compiled only under its own tag, so `go build`, `go vet`
and `golangci-lint` must each run twice or half the tree goes unchecked. `gopls` resolves one
variant, so the other rots unnoticed. And the binary the suite exercises is not the binary
that ships, which is the thing an integration suite exists to prevent.

Redirection needs none of that, because there is no second implementation to exclude. The
guarantee is stronger by construction: production cannot link out fake code that was never
written.

The cost, stated plainly: a base URL is configuration, so a misconfigured deployment could
point at the wrong host. That is the same class of risk as the database URL, carries the same
mitigation — default to the real value, treat the flag as sensitive — and is not new.

## 3. The fake server

`backend/cmd/fakethirdparty` is a standalone binary. It is never imported by `cmd/api`; the
two communicate only over HTTP, exactly as the API and github.com would.

It mimics each provider's **real wire format**, closely enough that the real client parses it:
GitHub's OAuth token exchange, `/user`, PR and repo metadata, and reviews; Google's OIDC token
and userinfo endpoints; Anthropic's Batch create/poll/results; Resend's send.

### It holds no data of its own

Every byte it returns comes from the fixture. The binary knows routes, status codes and
response envelopes; it knows nothing about alice, or repository star counts, or what the model
says about PR #42. A fixture gains a `third_party` block:

```json
"third_party": {
  "github": {
    "oauth_codes":   { "code-alice": { "id": 1001, "login": "alice", "email": "..." } },
    "repositories":  { "kubernetes/kubernetes": { "stargazers_count": 104000, ... } },
    "pull_requests": { "kubernetes/kubernetes#42": { ... } }
  },
  "google":    { "oauth_codes": { "code-carol": { "email": "...", "email_verified": true } } },
  "anthropic": { "judgements":  { "kubernetes/kubernetes#42:golang": { ... } } }
}
```

`POST /_load` replaces the server's entire state with that block, and the harness calls it
before each fixture — the same isolation rule the database follows, for the same reason. A
route with no matching fixture entry returns the provider's real 404 rather than an
invention, so a fixture that forgot to declare a PR fails as a missing PR instead of passing
on a default.

This keeps expected values reviewable in one place and stops the fake server from quietly
becoming a second source of truth about test data.

`GET /_sent/...` runs the other way: it exposes what the API sent, so a fixture can assert
that confirming a shortlist produced exactly one email carrying the payment-verified
disclosure (ADR-0002 §5).

Neither endpoint is reachable from the API binary, because the API is an HTTP client of this
server and never the reverse.

## 4. What this removes from stage 3

`e2e/control.go` is rewritten against `/_load` and `/_sent`. `ApplyFakes` and
`RunControlAction` go: they were written for per-step priming of in-memory fakes, and both are
already called from nowhere.

What replaces them is the `third_party` block of §3. No fixture declares third-party data
today in any form, which is why the AI-judgement and email fixtures currently have no way to
say what should come back — that gap is the reason this section exists, and it closes when
the block lands.

`e2e.sh` starts the fake server alongside Postgres and tears both down.

## 5. Time, without a fake clock

Fixtures need a lapsed availability window (ADR-0008) and an expired refresh token. Neither
needs the clock to move: the window is 30 days and a refresh token is 30 days, so a fixture
seeds `last_seen_at` 31 days back and the **real** clock reads it as lapsed. Controlling time
by choosing what you write is simpler than controlling it by intercepting `Now()`, and it
leaves no fake clock in the tree.

The one case it does not cover is a 15-minute access token expiring mid-run.
`--access-token-ttl` handles that — legitimate production config, set to `1s` in the one
fixture that needs it.

## Consequences

- No build tags, no `--adapters=fake`, no control listener inside the API, no fake Go type
  anywhere in `internal/`.
- One binary, one build, one lint pass. The suite exercises what ships.
- Real adapter HTTP handling gains e2e coverage it would otherwise never have.
- The fake server must track provider wire formats. Drift is possible, and this is the real
  price of the approach — a provider changing its response shape breaks production while e2e
  stays green. Contract tests against recorded live responses are the mitigation, and are out
  of scope here.
- `cmd/fakethirdparty` is a second binary to build and run, and it is test infrastructure that
  ships in the repository.

## Open questions

_None._
