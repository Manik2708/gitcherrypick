#!/usr/bin/env bash
#
# Runs the integration suite: start Postgres, apply the schema, run the tests,
# tear down. Same command locally and in CI.
#
#   backend/scripts/e2e.sh                    everything
#   backend/scripts/e2e.sh -run TestFixtures/claims    a subset
#   E2E_KEEP=1 backend/scripts/e2e.sh         leave Postgres up to inspect a failure
#
# Teardown is unconditional. A suite that leaves a container behind on failure
# poisons the next run, which is when you can least afford a confusing result.

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"

DB_PORT=55432
export E2E_DATABASE_URL="postgres://gitcherrypick:e2e@127.0.0.1:${DB_PORT}/gitcherrypick_e2e?sslmode=disable"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror\033[0m %s\n' "$*" >&2; exit 1; }

compose() { docker compose --file "${COMPOSE_FILE}" "$@"; }

teardown() {
  local status=$?
  if [[ "${E2E_KEEP:-0}" == "1" ]]; then
    warn "E2E_KEEP=1 — leaving Postgres up on port ${DB_PORT}"
    warn "  psql '${E2E_DATABASE_URL}'"
    warn "  stop it with: docker compose -f ${COMPOSE_FILE} down -v"
  else
    log "tearing down"
    compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  exit "${status}"
}
# Covers success, failure, and Ctrl-C alike.
trap teardown EXIT INT TERM

# --- preconditions -----------------------------------------------------------

command -v docker >/dev/null 2>&1 || die "docker is not installed"
docker info >/dev/null 2>&1 || die "the Docker daemon is not running — start Docker Desktop"
command -v go >/dev/null 2>&1 || die "go is not installed"

# --- postgres ----------------------------------------------------------------

log "starting postgres"
# Down first: a container left by a killed run would otherwise be reused with
# whatever state it had.
compose down --volumes --remove-orphans >/dev/null 2>&1 || true
compose up --detach --wait --wait-timeout 90 postgres \
  || die "postgres did not become healthy — 'docker compose -f ${COMPOSE_FILE} logs postgres'"

# --wait honours the compose healthcheck, so by here the server accepts queries.
# Verified rather than assumed, because a false start here produces failures
# that look like test bugs.
docker compose --file "${COMPOSE_FILE}" exec -T postgres \
  psql -U gitcherrypick -d gitcherrypick_e2e -qtAc 'select 1' >/dev/null \
  || die "postgres reported healthy but will not answer a query"
log "postgres ready on ${DB_PORT}"

# --- extensions --------------------------------------------------------------

psql_run() {
  docker compose --file "${COMPOSE_FILE}" exec -T postgres \
    psql -U gitcherrypick -d gitcherrypick_e2e -q -v ON_ERROR_STOP=1 "$@"
}

# Extensions live in their own schema, not in public.
#
# Each fixture runs in a schema of its own so cases cannot see each other's
# rows, and its search_path is "<case>", ext. If the extensions were in public
# then public would have to be on the search_path too — and anything the case
# did not create itself would silently fall through to whatever another case
# had left there, which is exactly the isolation this design exists to provide.
log "installing extensions"
psql_run -c 'CREATE SCHEMA IF NOT EXISTS ext' \
         -c 'CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA ext' \
         -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA ext' >/dev/null \
  || die "could not install extensions"

# --- schema check ------------------------------------------------------------

# The Go runner applies the DDL itself, once per case. Applying it here as well
# is not redundant: a broken .schema file fails once, here, with psql naming the
# statement — rather than 31 times inside Go with the same error each time.
#
# Applied in RFC order; each file is a delta and they are not commutative. Each
# goes through psql's autocommit rather than one transaction, because the
# ALTER TYPE ... ADD VALUE statements in RFC-0007 and RFC-0008 fail with
# "unsafe use of new value" when batched with the statements that use them.
log "checking schema"
psql_run -c 'DROP SCHEMA IF EXISTS _ddl_check CASCADE' -c 'CREATE SCHEMA _ddl_check' >/dev/null

shopt -s nullglob
for schema in "${BACKEND_DIR}"/../rfc/RFC-*.schema; do
  name="$(basename "${schema}")"
  # shellcheck disable=SC2002
  if ! { echo 'SET search_path TO _ddl_check, ext;'; cat "${schema}"; } | psql_run >/dev/null; then
    die "schema failed: ${name}"
  fi
  printf '     %s\n' "${name}"
done
shopt -u nullglob

tables=$(docker compose --file "${COMPOSE_FILE}" exec -T postgres \
  psql -U gitcherrypick -d gitcherrypick_e2e -qtAc \
  "select count(*) from information_schema.tables where table_schema='_ddl_check'")
[[ "${tables}" -gt 0 ]] || die "schema applied but no tables exist"

# Dropped so nothing shared survives into the run. The enums and other types it
# created are database-wide and stay, which is what the per-case DDL expects.
psql_run -c 'SET client_min_messages TO warning' -c 'DROP SCHEMA _ddl_check CASCADE' >/dev/null 2>&1
log "schema valid — ${tables} tables"

# --- tests -------------------------------------------------------------------

log "running the suite"
cd "${BACKEND_DIR}"

# -count=1 defeats the test cache. A cached pass from before a fixture changed
# is worse than no result, because it looks like a real one.
go test ./e2e/... -count=1 -v "$@"
