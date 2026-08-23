# RFC-0011 — Access token claims and signing-key rotation

**Status:** Approved 2026-08-23 · **Binding form:**
[ADR-0011](../adr/ADR-0011-access-token-claims-and-key-rotation.md)
· **Schema:** _(no delta)_
· **Amends:** ADR-0002 §8 (elaborates; contradicts nothing)

## Summary

ADR-0002 §8 specifies the access token in seven words — "Ed25519 JWT, 15 min" — and stops.
It does not say what the token asserts or how the signing key is replaced. Both must be
decided before `TokenIssuer` can be implemented, because the middleware has to verify exactly
what the issuer signs.

## 1. Claims are minimal, and this is forced

`domain.Principal` is not an identifier. It carries a whole `*Contributor`, `*Hirer` or
`*Admin` — display name, avatar, availability, `OverallScore`, `GeneralistScore`. A 15-minute
bearer token cannot carry that, so **the middleware loads the entity from Postgres on every
authenticated request regardless of what the token says.**

Given that read must happen, any claim beyond identity is redundant with it and staler than
it. So the token asserts identity and nothing else:

| Claim  | Value                               |
| ------ | ----------------------------------- |
| `iss`  | `gitcherrypick`                     |
| `sub`  | the account id                      |
| `kind` | `contributor` \| `hirer` \| `admin` |
| `iat`  | issued at                           |
| `exp`  | `iat + 15m` (ADR-0002 §8)           |
| `jti`  | random, for log correlation         |

`kind` is present because the three account types share no table (ADR-0002): without it the
middleware cannot know which one to read, and probing all three would let a contributor id
collide into a hirer lookup.

There is a second reason beyond redundancy, and it is the stronger one. Embedding
organization membership or hiring capability would mean a **suspended organization keeps
acting for up to 15 minutes**, because the token still asserts a capability the database has
revoked. `RequireHiringCapability` (ADR-0002) reads current state precisely so revocation is
immediate. A claim would quietly undo that.

The cost is one indexed lookup per request. That is the correct trade against a 15-minute
revocation window.

## 2. `kid` ships now, not later

The signing key must be replaceable — on compromise, on rotation policy, or on staff
turnover. Rotation means two keys are valid at once: tokens minted before the switch must
verify until they expire, or every signed-in user is logged out at the moment of rotation.

Deferring this is not cheaper. Tokens issued without a `kid` header cannot be attributed to a
key, so introducing rotation later requires a verifier that tries every key against
unattributed tokens — which is the rollover mechanism the deferral was meant to avoid, plus a
migration. The header costs about twenty lines today.

So: every token carries `kid`. The verifier resolves `kid` against a keyset, and rejects a
token whose `kid` it does not know rather than trying keys in turn.

## 3. Key material

One key signs; zero or more verify. Rotation is: add the new key as verify-only, switch
signing to it, and drop the old one once every token it signed has expired — fifteen minutes
later.

Keys never enter the database. The private key is Ed25519 seed material, and putting it beside
the data it protects means one SQL injection takes both.

Key material comes from **files on disk**, named by flag:

```
--signing-key=/run/secrets/ed25519-2026-08.pem     # exactly one, PKCS#8 Ed25519 private key
--verify-key=2026-07:/run/secrets/ed25519-2026-07.pub   # repeatable, kid:path
--verify-key=2026-08:/run/secrets/ed25519-2026-08.pub
```

The signing key's own `kid` is required and comes from a matching `--verify-key` entry, so a
key can never sign tokens nothing can verify. Startup fails — loudly, before the listener
binds — if the signing key is absent, unparseable, or has no corresponding verify entry.

Files rather than flag or environment values because the PEM then never reaches the process
table, a crash dump, or anything that logs argv. Kubernetes secrets, Docker secrets and
systemd credentials all present as files, so the same flag works unchanged across them. The
e2e harness writes a throwaway keypair to a temp directory and passes its path.

There is deliberately **no generate-if-absent fallback**. It would make a production that
forgot the flag start successfully and silently, then sign with a key nobody holds and log
every user out on each deploy — or, across replicas, mint tokens the other replicas reject.
Refusing to boot converts that into an error at the only moment it is cheap to fix.

## Open questions

_None._
