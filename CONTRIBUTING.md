# Contributing

Thanks for looking. This page covers how to get a change merged; the
[README](README.md) covers how to run the project.

## Finding something to work on

Start with an issue labelled
[`good first issue`](https://github.com/Manik2708/gitcherrypick/labels/good%20first%20issue).
Each one names the files involved and says what "done" means.

Comment on the issue before you start so two people do not do the same work. If you want
to work on something that has no issue, open one first and wait for a reply.

## Making a change

1. Fork the repository and branch from `main`.
2. Set up once: `make setup`, then `(cd frontend && npm ci)` if you are touching the
   frontend.
3. Make the change, with tests.
4. Run `make check`. It needs no Docker and is what CI's `check` job runs.
5. If you touched a repository or anything an HTTP response depends on, also run
   `make check-all`. It needs a running Docker daemon.
6. If you touched `frontend/`, run `npm run typecheck`, `npm run lint` and `npm test`
   there.
7. Open a pull request against `main` and link the issue.

Keep a pull request to one issue. A fix and an unrelated tidy-up are two pull requests.

### Commit messages

A type prefix, an optional scope, and a short summary in the imperative:

```
feat: public openings and role-driven search
docs(adr): ADR-0020 public openings
test(domain): table tests for ParsePRURL
```

## What needs a design decision first

The project is built design-first. A feature is proposed in an RFC, approved, and
promoted to an ADR of the same number; only then is it implemented. [`rfc/README.md`](rfc/README.md)
and [`adr/README.md`](adr/README.md) describe the process.

That gives three rules for a code contribution:

- **Do not edit an approved RFC or an ADR.** They are the record of what was decided. If
  one is wrong or two disagree, open an issue.
- **Do not change behaviour an ADR specifies.** A new endpoint, a changed response shape,
  a schema change or a scoring constant needs an RFC, not a pull request. Open an issue
  describing what you want and why.
- **Do not edit a fixture in `backend/e2e/fixtures/` to make a test pass.** The fixtures
  are the contract. If one looks wrong, say so in an issue.

If you find the code disagreeing with its own comment or with an ADR, report it rather
than picking a side. Several open issues ask for exactly that.

## Backend conventions

`backend/.golangci.yml` enforces the layering; the rest is checked in review.

- **Three layers: controllers, services, repositories.** A controller serialises,
  deserialises and validates IO, and holds no business logic. A service holds the
  business logic and does not know which database is behind the repository. A repository
  translates between domain models and storage rows.
- **Dependencies cross a layer boundary as an interface** from `internal/port`, never as
  a concrete type.
- **`internal/domain` imports nothing else from `internal/`.** `internal/port` names no
  vendor: no `pgx`, no `chi`, no model provider.
- **Every struct is a named type.** No anonymous structs, including in table tests:
  declare a case type and use a slice of it.
- **Errors are wrapped with `%w` and carry context.** No bare `return err` at a layer
  boundary.
- **Configuration comes from CLI flags** wired with `cobra`. No `os.Getenv` in business
  code.
- **Only `backend/cmd/<binary>/main.go` is a `main` package.**
- **Formatting is `gofmt` and `goimports`.** Run `make fmt`; do not hand-format.

### Tests

- **A unit test mocks the layer below and nothing else.** Controller tests mock services;
  service tests mock repositories. No database and no network.
- **Mocks are generated, never hand-written.** After changing an interface in
  `internal/port`, run `make mocks` and commit the result.
- **The coverage target is 95%.** To see what a package is missing:

  ```bash
  cd backend
  go test -coverprofile=cover.out ./internal/service/
  go tool cover -func=cover.out      # per function
  go tool cover -html=cover.out      # the exact lines
  ```

- Repository tests run against a real Postgres through `make db-test`. Run directly with
  `go test` they skip.

## Frontend conventions

- API endpoint strings live in one file, `frontend/src/api/endpoints.ts`. Do not write a
  path anywhere else.
- The API base URL comes from `.env`; `frontend/.env.sample` is the committed template.
  Leave it unset for local development.
- Logic worth testing lives in `frontend/src/logic/`, not inside a component.
- A missing value is never rendered as zero. See `frontend/src/logic/format.ts`.
- A frontend problem is not fixed by changing the backend. If the API does not give the
  client what it needs, open an issue.

## Other files

- Markdown, JSON, TypeScript and CSS are formatted by Prettier: `make fmt`.
- Repository tooling in `scripts/` is Python.
- Never commit a secret. `.env` files are ignored; nothing in this repository needs a
  real credential to run or test.

## Reporting a bug

Open an issue with what you did, what you expected and what happened. For a backend bug,
a failing test is the most useful report there is.

Please do not open a public issue for a security problem. Email the maintainer through
the address on their [GitHub profile](https://github.com/Manik2708) instead.
