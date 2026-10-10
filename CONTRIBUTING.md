# Contributing to GitCherryPick

## Licence and CLA

GitCherryPick is licensed under the [GNU AGPL-3.0](LICENSE).

Every contributor signs the [Contributor License Agreement](CLA.md) once, before their
first pull request is merged. You keep ownership of your work; the CLA grants the
maintainer the right to distribute it and to relicense the project.

To sign, comment on your pull request:

> I have read the CLA Document and I hereby sign the CLA

The `cla` check on the pull request turns green once every commit author has signed.

## How work is organised

Work moves through fixed stages with approval gates — see [CLAUDE.md](CLAUDE.md). Read the
relevant `adr/` before changing behaviour; the ADR is binding.

- A new feature starts as an RFC in `rfc/`, not as code.
- Integration fixtures in `backend/e2e/` are never edited to make an implementation pass.
- API mismatches and design gaps are raised as an issue, not fixed across layers.

## Before opening a pull request

```bash
make setup    # tools, deps, venv
make check    # everything CI's `check` job runs, no Docker needed
```
