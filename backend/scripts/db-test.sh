#!/usr/bin/env bash
#
# Runs the repository integration suite against a real Postgres.
#
#   backend/scripts/db-test.sh
#   backend/scripts/db-test.sh --keep-db
#   backend/scripts/db-test.sh --display-logs-on-failure
#   backend/scripts/db-test.sh --keep-db --display-logs-on-failure -run TestUserRepository
#
# Flags
#
#   --keep-db                   Leave Postgres running after the tests finish,
#                               and BLOCK until you stop this script (Ctrl-C).
#                               The container is torn down on the way out, so a
#                               kept database never outlives the terminal that
#                               asked for it.
#
#   --display-logs-on-failure   Print the Postgres log if the tests fail.
#                               Default OFF: a passing run should say nothing,
#                               and a failing one should not bury the assertion
#                               under a wall of SQL unless you asked for it.
#
# Anything not recognised is passed through to `go test`, so -run, -v and
# -count work as usual.
#
# Only the database runs. The API and the evaluator are not started, because a
# repository test that needed the application running could not tell a mapping
# bug from a wiring bug.

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.db.yml"

DB_PORT=55433
export DB_TEST_DATABASE_URL="postgres://gitcherrypick:db@127.0.0.1:${DB_PORT}/gitcherrypick_db_test?sslmode=disable"

KEEP_DB=0
DISPLAY_LOGS_ON_FAILURE=0
GO_TEST_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep-db)                  KEEP_DB=1 ;;
    --display-logs-on-failure)  DISPLAY_LOGS_ON_FAILURE=1 ;;
    -h|--help)
      sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)                          GO_TEST_ARGS+=("$1") ;;
  esac
  shift
done

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror\033[0m %s\n' "$*" >&2; exit 1; }

compose() { docker compose --file "${COMPOSE_FILE}" "$@"; }

TESTS_FAILED=0

show_logs() {
  printf '\n\033[1;33m--- postgres log ---\033[0m\n'
  # Same process, not a file to go and read afterwards: the log is only useful
  # while the failure it explains is still on screen.
  compose logs --no-color --timestamps postgres || true
  printf '\033[1;33m--- end of log ---\033[0m\n\n'
}

teardown() {
  local status=$?

  if [[ "${TESTS_FAILED}" -eq 1 && "${DISPLAY_LOGS_ON_FAILURE}" -eq 1 ]]; then
    show_logs
  fi

  log "stopping postgres"
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  exit "${status}"
}
# Covers success, failure and Ctrl-C alike. A suite that leaves a container
# behind poisons the next run, which is when you can least afford confusion.
trap teardown EXIT INT TERM

# --- preconditions -----------------------------------------------------------

command -v docker >/dev/null 2>&1 || die "docker is not installed"
docker info >/dev/null 2>&1 || die "the Docker daemon is not running — start Docker Desktop"
command -v go >/dev/null 2>&1 || die "go is not installed"

# --- postgres ----------------------------------------------------------------

log "starting postgres (database only)"
compose down --volumes --remove-orphans >/dev/null 2>&1 || true
compose up --detach --wait --wait-timeout 90 postgres \
  || die "postgres did not become healthy — 'docker compose -f ${COMPOSE_FILE} logs postgres'"

compose exec -T postgres \
  psql -U gitcherrypick -d gitcherrypick_db_test -qtAc 'select 1' >/dev/null \
  || die "postgres reported healthy but will not answer a query"
log "postgres ready on ${DB_PORT}"

# --- schema ------------------------------------------------------------------

# Extensions first, in their own schema, then the DDL into public. Unlike the
# e2e suite there is no schema-per-case here: these tests share one schema and
# TRUNCATE between each, which is faster and is what the isolation requirement
# actually needs.
log "applying schema"
compose exec -T postgres psql -U gitcherrypick -d gitcherrypick_db_test -q -v ON_ERROR_STOP=1 \
  -c 'CREATE SCHEMA IF NOT EXISTS ext' \
  -c 'CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA ext' \
  -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA ext' \
  -c 'ALTER DATABASE gitcherrypick_db_test SET search_path TO public, ext' >/dev/null \
  || die "could not install extensions"

shopt -s nullglob
for schema in "${BACKEND_DIR}"/../rfc/RFC-*.schema; do
  name="$(basename "${schema}")"
  # Each file through psql's autocommit rather than one transaction: the
  # ALTER TYPE ... ADD VALUE statements fail with "unsafe use of new value"
  # when batched with the statements that use them.
  if ! { echo 'SET search_path TO public, ext;'; cat "${schema}"; } \
        | compose exec -T postgres psql -U gitcherrypick -d gitcherrypick_db_test -q -v ON_ERROR_STOP=1 >/dev/null; then
    die "schema failed: ${name}"
  fi
  printf '     %s\n' "${name}"
done
shopt -u nullglob

tables=$(compose exec -T postgres psql -U gitcherrypick -d gitcherrypick_db_test -qtAc \
  "select count(*) from information_schema.tables where table_schema='public'")
[[ "${tables}" -gt 0 ]] || die "schema applied but no tables exist"
log "schema applied — ${tables} tables"

# --- tests -------------------------------------------------------------------

log "running the repository suite"
cd "${BACKEND_DIR}"

# -count=1 defeats the test cache. A cached pass from before a query changed is
# worse than no result, because it looks like a real one.
#
# The `|| TESTS_FAILED=1` is why `set -e` does not fire here: a failing suite is
# an outcome this script handles, not an error that should abort it — otherwise
# --keep-db would never get the chance to keep anything.
# ${arr[@]+"${arr[@]}"} rather than "${arr[@]}": under `set -u` an empty array
# is an unbound variable in bash 3, which ships on macOS.
if go test ./internal/repository/... -count=1 ${GO_TEST_ARGS[@]+"${GO_TEST_ARGS[@]}"}; then
  log "tests passed"
else
  TESTS_FAILED=1
  warn "tests FAILED"
fi

# --- keep-db -----------------------------------------------------------------

if [[ "${KEEP_DB}" -eq 1 ]]; then
  if [[ "${TESTS_FAILED}" -eq 1 && "${DISPLAY_LOGS_ON_FAILURE}" -eq 1 ]]; then
    # Show them now rather than at exit, so they are on screen while the
    # database is still up and worth poking at.
    show_logs
    DISPLAY_LOGS_ON_FAILURE=0
  fi

  printf '\n\033[1;32m==>\033[0m postgres is still running — inspect it with:\n\n'
  printf '      psql '\''%s'\''\n\n' "${DB_TEST_DATABASE_URL}"
  printf '    Whatever the last test left behind is still there; the suite\n'
  printf '    truncates BEFORE each test, never after.\n\n'
  printf '    Press Ctrl-C to stop the database and exit.\n\n'

  # Block until interrupted.
  #
  # A short sleep in a loop rather than one long one: bash runs `sleep` as a
  # child and does not act on a trapped signal until the foreground child
  # returns, so `sleep 3600` would leave Ctrl-C apparently ignored for up to an
  # hour. One second costs nothing and makes the trap feel immediate.
  while true; do sleep 1; done
fi

exit "${TESTS_FAILED}"
