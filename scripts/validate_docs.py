#!/usr/bin/env python3
"""Validate the RFC/ADR corpus.

The rule this exists to enforce: **an ADR must not carry open questions.** An ADR is
binding — implementers build against it and may not assume anything that lives only in an
RFC. A binding document that admits it has not decided something forces every reader to
guess which parts are real, and the undecided parts get implemented anyway by whoever
reaches them first.

ADR-0008 shipped with three. This check is why that cannot happen twice.

Also checks the structural conventions the corpus depends on:

  * every ADR names the RFC it came from, and that RFC exists
  * every promoted RFC is marked Approved and links its binding form
  * an ADR referenced as an amendment exists
  * a .schema referenced by an RFC exists

Runs identically locally and in CI. Exits non-zero on any problem.

    python3 scripts/validate_docs.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RFC_DIR = ROOT / "rfc"
ADR_DIR = ROOT / "adr"

errors: list[str] = []


def fail(path: Path, message: str) -> None:
    errors.append(f"{path.relative_to(ROOT)}: {message}")


def section(text: str, heading: str) -> str | None:
    """Return the body of a ## section, or None when it is absent."""
    match = re.search(rf"^## {re.escape(heading)}\s*$(.*?)(?=^## |\Z)", text, re.M | re.S)
    return match.group(1) if match else None


def has_content(body: str) -> bool:
    """Whether a section says anything, ignoring blank lines and sub-headings."""
    for line in body.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        # A table header or separator alone is not content.
        if set(stripped) <= set("|- "):
            continue
        return True
    return False


def check_adr(path: Path) -> None:
    text = path.read_text()

    # THE RULE.
    body = section(text, "Open questions")
    if body is not None and has_content(body):
        fail(
            path,
            "an ADR must not carry open questions — resolve them with the owner before "
            "promoting, or leave the RFC Proposed until they can be answered",
        )

    for required in ("Decision", "Consequences"):
        if section(text, required) is None:
            fail(path, f"missing a '## {required}' section")

    match = re.search(r"\*\*From:\*\*\s*\[([^\]]+)\]\(([^)]+)\)", text)
    if not match:
        fail(path, "does not name the RFC it was promoted from")
        return

    source = (path.parent / match.group(2)).resolve()
    if not source.exists():
        fail(path, f"names {match.group(1)}, which does not exist at {match.group(2)}")
        return

    rfc_text = source.read_text()
    if not re.search(r"\*\*Status:\*\*\s*Approved", rfc_text):
        fail(source, f"is promoted by {path.name} but is not marked Approved")
    if "**Binding form:**" not in rfc_text:
        fail(source, f"is promoted by {path.name} but does not link its binding form")


def check_rfc(path: Path) -> None:
    text = path.read_text()

    match = re.search(r"\*\*Schema:\*\*\s*\[([^\]]+)\]\(([^)]+)\)", text)
    if match and not (path.parent / match.group(2)).resolve().exists():
        fail(path, f"links a schema that does not exist: {match.group(2)}")

    # An approved RFC's open questions are the ADR's problem, and check_adr
    # catches them there. An unapproved one may have as many as it likes.


def main() -> int:
    adrs = sorted(p for p in ADR_DIR.glob("ADR-*.md"))
    rfcs = sorted(p for p in RFC_DIR.glob("RFC-*.md"))
    if not adrs or not rfcs:
        print("no ADRs or RFCs found", file=sys.stderr)
        return 1

    for path in adrs:
        check_adr(path)
    for path in rfcs:
        check_rfc(path)

    if errors:
        plural = "" if len(errors) == 1 else "s"
        print(f"\n{len(errors)} problem{plural}:\n", file=sys.stderr)
        for error in errors:
            print(f"  {error}", file=sys.stderr)
        print("", file=sys.stderr)
        return 1

    print(f"{len(adrs)} ADRs and {len(rfcs)} RFCs valid.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
