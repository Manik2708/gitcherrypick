# ADR-0011 — Access token claims and signing-key rotation

**Status:** Accepted · **From:** [RFC-0011](../rfc/RFC-0011-access-token-claims-and-key-rotation.md)
· **Schema:** _(no delta)_
· **Amends:** ADR-0002 §8 — elaborates it, contradicts nothing.

## Decision

1. **The access token asserts identity and nothing else.**

   | Claim  | Value                               |
   | ------ | ----------------------------------- |
   | `iss`  | `gitcherrypick`                     |
   | `sub`  | the account id                      |
   | `kind` | `contributor` \| `hirer` \| `admin` |
   | `iat`  | issued at                           |
   | `exp`  | `iat + 15m` (ADR-0002 §8)           |
   | `jti`  | random, for log correlation         |

2. **No authorization claim is ever embedded** — not organization membership, not hiring
   capability, not verification state.

3. **Every token carries a `kid` header.** The verifier resolves it against a keyset and
   rejects an unknown `kid` outright rather than trying keys in turn.

4. **Key material comes from files**, never from the database and never from a flag or
   environment value holding the PEM itself:

   ```
   --signing-key=/run/secrets/ed25519-2026-08.pem        exactly one, PKCS#8 Ed25519 private key
   --verify-key=2026-07:/run/secrets/ed25519-2026-07.pub repeatable, kid:path
   --verify-key=2026-08:/run/secrets/ed25519-2026-08.pub
   ```

5. **Startup fails before the listener binds** if the signing key is missing, unparseable, or
   has no matching `--verify-key` entry. **There is no generate-if-absent fallback.**

## Implementation

### Minimal claims are forced, not preferred

`domain.Principal` carries a whole `*Contributor`, `*Hirer` or `*Admin` — display name,
availability, `OverallScore`, `GeneralistScore`. A 15-minute bearer token cannot carry that,
so the middleware reads the entity from Postgres on **every** authenticated request regardless
of what the token says. Any claim beyond identity is therefore redundant with a read that has
to happen anyway, and staler than it.

`kind` survives because the three account types share no table (ADR-0002). Without it the
middleware cannot know which table to read, and probing all three would let a contributor id
collide into a hirer lookup.

### `port.TokenIssuer` changed to match

The interface as written in stage 3 could not express this decision, so promoting it required
correcting it:

- `Issue` took a whole `domain.Principal` and `Verify` returned one. Both now use
  `port.AccessClaims{Subject, Kind}` — precisely what the token carries.
- `NewRefreshToken` and `HashRefreshToken` are gone. They duplicated `TokenMinter`, whose
  shape is identical, while the interface comment claimed "only the access side lives here".
  One generator means one place where entropy and hash algorithm are decided, and the refresh
  token is the more sensitive of the two.

The first change fixed a live bug rather than a hypothetical one. `AuthService.Refresh` called
`Issue(ctx, domain.Principal{Kind: session.Kind}, …)` with no entity attached, so every
refreshed token would have carried an empty `sub` — `session.PrincipalID` was in scope and
unused. Passing a whole Principal where only identity is needed is what let a hollow one
type-check; `AccessClaims` makes the omission impossible to express.

`domain.Principal.Subject()` replaces the service-local `principalID` helper, which
dereferenced `p.Contributor.ID` without a nil check.

### The stronger reason: revocation

Embedding hiring capability would let a **suspended organization keep acting for up to fifteen
minutes**, because the token still asserts what the database has revoked.
`RequireHiringCapability` reads current state precisely so revocation is immediate; a claim
would quietly undo that.

The cost is one indexed lookup per request, which is the correct trade against a 15-minute
revocation window.

### Why `kid` now

Rotation means two keys are valid at once — tokens minted before the switch must verify until
they expire, or every signed-in user is logged out at the moment of rotation. Tokens issued
without a `kid` cannot be attributed to a key, so adding rotation later needs a verifier that
tries every key against unattributed tokens: the exact rollover machinery the deferral was
meant to avoid, plus a migration. The header costs about twenty lines today.

### Why files, and why no fallback

A PEM passed as a flag or environment value reaches the process table, crash dumps, and
anything that logs argv. Kubernetes secrets, Docker secrets and systemd credentials all
present as files, so one flag shape works across all of them unchanged.

Generate-if-absent would let a production that forgot the flag boot successfully and silently,
then sign with a key nobody holds — logging every user out on each deploy, or across replicas
minting tokens the other replicas reject. Refusing to boot converts that into an error at the
only moment it is cheap to fix.

### Rotation procedure

Add the new key as verify-only; switch `--signing-key` to it; drop the old key once every
token it signed has expired — fifteen minutes later.

## Steps

1. `internal/adapter/crypto`: `TokenIssuer` over Ed25519 with the claim set above, a keyset
   loaded from files, and `kid` on every token.
2. Reject at load: unknown `kid`, `alg` other than `EdDSA`, absent `exp`, unparseable key.
3. `cmd/api`: `--signing-key`, repeatable `--verify-key`, `--access-token-ttl` (default 15m).
4. Middleware verifies, then loads the principal from Postgres by `sub` and `kind`.
5. The e2e harness writes a throwaway keypair to a temp directory and passes its path.

## Consequences

- One indexed read per authenticated request. Revocation is immediate rather than eventual.
- Rotation is an operational procedure with no code change and no forced sign-out.
- Key files must be mounted before the API starts, in every environment including local dev —
  there is no zero-config path. `bootstrap.sh` should generate a dev keypair.
- The token is opaque to the frontend beyond `sub`, `kind` and `exp`. Anything the UI needs
  about the current user comes from an endpoint, not from decoding the token.

## Amendments

None.
