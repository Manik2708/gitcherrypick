# GitCherryPick

GitCherryPick is a platform where open-source contributors prove their skills with evidence, allowing recruiters to cherry-pick from a ranked pool.

## How it Works

- **Claims:** A contributor signs in with GitHub and submits an evidence bundle called a claim. This includes up to five of their best merged PRs, optional supporting projects, and the skills those PRs demonstrate. One AI model call judges every PR against every declared skill.
- **Primary and Secondary Skills:** Skill standing is derived from evidence, not self-declared. Having five distinct evidencing PRs makes a skill _primary_, meaning it is ranked and searchable. Having one to four PRs makes a skill _secondary_, which contributes to the score and is visible, but is never ranked. Promotion to primary happens automatically at the fifth distinct PR.
- **Hirers:** Hirers are separately verified accounts. Scorecards are gated, and contributors can only see their own rank.

## Project Status

The project is currently at **Stage 4 — Core backend**. Stages 1 through 3 (Planning, Evaluation design, and Integration tests) are approved. There are 31 integration test fixtures written that are currently failing (RED), and the current implementation work aims to turn them green.

**What runs today:**

- A local development environment can be spun up using `backend/scripts/dev.sh`.
- This environment successfully starts a PostgreSQL database, the core API, and a `fakethirdparty` service that stands in for GitHub, Google, and Resend.
- The complete OAuth sign-in flow works locally against the stand-in service, meaning no real GitHub credentials are required to test authentication.
- A client application can be built against this API for every screen, except for live scores appearing automatically.

**What does not run:**

- The evaluator backend is not currently running.
- Any submitted claim simply sits in a queue because the model call for evaluating claims is currently just a stub.
