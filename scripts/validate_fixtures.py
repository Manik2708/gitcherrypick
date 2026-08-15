#!/usr/bin/env python3
"""Validate the integration fixtures.

Two passes. The first checks every case against ``fixture.schema.json``. The second checks
the properties JSON Schema cannot express, because they span files:

  * every ``seed`` name resolves to a set that ``seed/`` actually provides
  * every ``as`` names a seeded principal, and the case loads the seed set defining it
  * every ``{{placeholder}}`` is bound by an earlier step or by seed data
  * case names are unique, so a Go subtest name is never ambiguous
  * the schema's own enums match what ``seed/`` provides, so the two cannot drift

Runs identically locally and in CI. Exits non-zero on any problem.

    python3 scripts/validate_fixtures.py
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

try:
    from jsonschema import Draft202012Validator
except ModuleNotFoundError:
    sys.exit(
        "jsonschema is not installed.\n"
        "  python3 -m venv .venv && .venv/bin/pip install -r scripts/requirements.txt\n"
        "  .venv/bin/python scripts/validate_fixtures.py"
    )

ROOT = Path(__file__).resolve().parent.parent
FIXTURES = ROOT / "backend/e2e/fixtures"
CASES = FIXTURES / "controllers"
SCHEMA = FIXTURES / "fixture.schema.json"
SEEDS = FIXTURES / "seed"

# 'anonymous' has no session; 'system' drives the harness rather than the HTTP surface.
RUNNER_PRINCIPALS = {"anonymous", "system"}

PLACEHOLDER = re.compile(r"\{\{([a-z0-9_.]+)\}\}")
AUTO_CAPTURE = re.compile(r"^[a-z][a-z0-9_]*_id$")

errors: list[str] = []


def fail(path: Path, message: str) -> None:
    errors.append(f"{path.relative_to(ROOT)}: {message}")


def read_json(path: Path) -> dict | None:
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        fail(path, f"is not valid JSON — {exc}")
        return None


def walk_json(node):
    """Yield every dict in a nested structure, including the root."""
    if isinstance(node, dict):
        yield node
        for value in node.values():
            yield from walk_json(value)
    elif isinstance(node, list):
        for item in node:
            yield from walk_json(item)


def load_seed_sets() -> tuple[set[str], dict[str, dict]]:
    """Return the set of loadable seed names and the raw documents behind them.

    A seed file is itself a set (``principals.json`` -> ``principals``) and may additionally
    declare named sets under ``_named_sets``.
    """
    names: set[str] = set()
    docs: dict[str, dict] = {}
    for path in sorted(SEEDS.glob("*.json")):
        doc = read_json(path)
        if doc is None:
            continue
        names.add(path.stem)
        docs[path.stem] = doc
        for named in doc.get("_named_sets", {}):
            names.add(named)
            docs[named] = doc
    return names, docs


def load_principals() -> set[str]:
    """Keys a step may authenticate AS.

    Organizations are seeded but are not principals — nobody signs in as a company. They are
    still referenceable as ``{{acme.id}}``, which ``seeded_row_keys`` covers.
    """
    doc = read_json(SEEDS / "principals.json") or {}
    keys = set()
    for group in ("contributors", "hirers", "admins"):
        for entry in doc.get(group, []):
            if key := entry.get("key"):
                keys.add(key)
    return keys


def check_schema_enums_match_seed(schema: dict, seed_names: set[str], principals: set[str]) -> None:
    """The schema hand-lists both closed sets. Keep them honest against seed/."""
    schema_seeds = set(schema["$defs"]["seedSet"]["enum"])
    for name in sorted(seed_names - schema_seeds):
        fail(SCHEMA, f"seed set '{name}' exists in seed/ but is missing from $defs.seedSet.enum")
    for name in sorted(schema_seeds - seed_names):
        fail(SCHEMA, f"$defs.seedSet.enum lists '{name}', which no file in seed/ provides")

    schema_principals = set(schema["$defs"]["principal"]["enum"])
    for name in sorted(principals - schema_principals):
        fail(SCHEMA, f"principal '{name}' is seeded but missing from $defs.principal.enum")
    for name in sorted(schema_principals - principals - RUNNER_PRINCIPALS):
        fail(SCHEMA, f"$defs.principal.enum lists '{name}', which principals.json does not seed")


def seeded_row_keys(case_seeds: list[str], seed_docs: dict[str, dict]) -> set[str]:
    """Keys a fixture may reference as {{key.field}} because a seed set writes the row."""
    keys: set[str] = set()
    for name in case_seeds:
        doc = seed_docs.get(name, {})
        for node in walk_json(doc):
            if "key" in node and isinstance(node["key"], str):
                keys.add(node["key"])
    return keys


def bindings_from_step(step: dict) -> set[str]:
    """Names a step binds for later steps: explicit captures plus the automatic *_id rule."""
    bound = set(step.get("capture", {}))
    body = step.get("expect", {}).get("body")
    if not isinstance(body, (dict, list)):
        return bound

    for node in walk_json(body):
        for key, value in node.items():
            if AUTO_CAPTURE.match(key):
                bound.add(key)
            # A nested object carrying an `id` binds `<key>_id`: a response containing
            # {"contact_request": {"id": ...}} makes {{contact_request_id}} available.
            if isinstance(value, dict) and "id" in value and re.fullmatch(r"[a-z][a-z0-9_]*", key):
                bound.add(f"{key}_id")

    # `POST /claims -> {"id": ...}` binds {{claim_id}} — the README's automatic rule.
    if isinstance(body, dict) and "id" in body:
        if match := re.match(r"^/([a-z-]+)", step["request"]["path"]):
            resource = match.group(1).rstrip("s").replace("-", "_")
            bound.add(f"{resource}_id")
    return bound


def check_case(path: Path, doc: dict, principals: set[str], seed_docs: dict[str, dict]) -> None:
    bound: set[str] = set()
    seeded = principals | seeded_row_keys(doc["seed"], seed_docs)

    for index, step in enumerate(doc["steps"]):
        at = f"steps[{index}]"
        actor = step.get("as")

        if actor and actor not in principals and actor not in RUNNER_PRINCIPALS:
            fail(path, f"{at}.as '{actor}' is not a seeded principal")
        elif actor in principals and "principals" not in doc["seed"]:
            fail(path, f"{at}.as '{actor}' needs the 'principals' seed set, which this case does not load")

        request = step["request"]
        scope = json.dumps([request["path"], request.get("body"), request.get("headers")])
        for ref in PLACEHOLDER.findall(scope):
            if ref in bound or ref.split(".")[0] in bound | seeded:
                continue
            fail(path, f"{at} uses {{{{{ref}}}}}, which no earlier step binds and no seed provides")

        bound |= bindings_from_step(step)


def main() -> int:
    schema = read_json(SCHEMA)
    if schema is None:
        print("\n".join(errors), file=sys.stderr)
        return 1

    seed_names, seed_docs = load_seed_sets()
    principals = load_principals()
    check_schema_enums_match_seed(schema, seed_names, principals)

    validator = Draft202012Validator(schema)
    files = sorted(CASES.rglob("*.json"))
    if not files:
        print(f"no fixtures found under {CASES.relative_to(ROOT)}", file=sys.stderr)
        return 1

    names: dict[str, Path] = {}
    for path in files:
        doc = read_json(path)
        if doc is None:
            continue

        structural = sorted(validator.iter_errors(doc), key=lambda e: list(e.absolute_path))
        if structural:
            for error in structural:
                where = "/".join(str(p) for p in error.absolute_path) or "(root)"
                fail(path, f"{where}: {error.message}")
            continue  # structural errors make the cross-file checks meaningless

        if previous := names.get(doc["name"]):
            fail(path, f"duplicate case name '{doc['name']}', also used by {previous.relative_to(ROOT)}")
        names[doc["name"]] = path

        check_case(path, doc, principals, seed_docs)

    if errors:
        plural = "" if len(errors) == 1 else "s"
        print(f"\n{len(errors)} fixture problem{plural}:\n", file=sys.stderr)
        for error in errors:
            print(f"  {error}", file=sys.stderr)
        print("", file=sys.stderr)
        return 1

    print(f"{len(files)} fixtures valid ({len(names)} cases, {len(seed_names)} seed sets).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
