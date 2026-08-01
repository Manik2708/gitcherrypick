---
description: Set up this machine for GitCherryPick and report any remaining gaps
---

Run the bootstrap script and report the result to the user:

```
!.claude/scripts/bootstrap.sh
```

Then, based on its output:

- If it exited non-zero, a required toolchain is missing. Tell the user exactly which
  item to install and stop — do not attempt to work around a missing Go or Node.
- If it reported open gaps, summarise them in one short list and say plainly what each
  one blocks:
  - **Docker daemon down** blocks integration tests (stage 3) and therefore all
    implementation. Ask the user to start Docker Desktop.
  - **Missing secrets** block only live runs against GitHub and Claude. They do *not*
    block the integration suite, which runs on fakes plus a Postgres container.
- If everything is ready, say so in one line and state which pipeline gate is currently
  open (see CLAUDE.md).

Do not start any implementation work as part of this command.
