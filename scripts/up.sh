#!/usr/bin/env bash
#
# The whole thing, in a browser.
#
#   scripts/up.sh              start the backend and the client
#   scripts/up.sh --reset      rebuild the database from the schema first
#   scripts/up.sh --plain      seed the minimal cast, without scored claims
#
# Starts backend/scripts/dev.sh (Postgres, the third-party stand-in, the API)
# and the Vite dev server, then prints where to go and who to sign in as.
# Ctrl-C stops both.
#
# Not a deployment. Stage 7 owns images and orchestration; this is the loop a
# developer works in.

set -Eeuo pipefail

REPO_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND_DIR="${REPO_DIR}/backend"
FRONTEND_DIR="${REPO_DIR}/frontend"

DEV_ARGS="--rich"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --reset) DEV_ARGS="${DEV_ARGS} --reset" ;;
    --plain) DEV_ARGS="${DEV_ARGS/--rich/}" ;;
    -h|--help) sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror\033[0m %s\n' "$*" >&2; exit 1; }

BACKEND_PID=""
FRONTEND_PID=""

teardown() {
  local status=$?
  log "stopping"
  [[ -n "${FRONTEND_PID}" ]] && kill "${FRONTEND_PID}" 2>/dev/null || true
  [[ -n "${BACKEND_PID}" ]] && kill "${BACKEND_PID}" 2>/dev/null || true
  # dev.sh has its own trap; give it a moment to stop the API and the stand-in.
  sleep 1
  pkill -f "${BACKEND_DIR}/.dev/api" 2>/dev/null || true
  pkill -f "${BACKEND_DIR}/.dev/fakethirdparty" 2>/dev/null || true
  exit "${status}"
}
trap teardown EXIT INT TERM

# --- backend -----------------------------------------------------------------

log "starting the backend"
# shellcheck disable=SC2086
"${BACKEND_DIR}/scripts/dev.sh" ${DEV_ARGS} >"${REPO_DIR}/.up-backend.log" 2>&1 &
BACKEND_PID=$!

for _ in $(seq 1 120); do
  curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1 && break
  # A dead starter means the failure is in the log, not in the wait.
  kill -0 "${BACKEND_PID}" 2>/dev/null || die "the backend failed to start:$(printf '\n'; tail -20 "${REPO_DIR}/.up-backend.log")"
  sleep 1
done
curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1 \
  || die "the backend never answered:$(printf '\n'; tail -20 "${REPO_DIR}/.up-backend.log")"

# --- frontend ----------------------------------------------------------------

if [[ ! -d "${FRONTEND_DIR}/node_modules" ]]; then
  log "installing client dependencies (first run only)"
  (cd "${FRONTEND_DIR}" && npm install >/dev/null 2>&1) || die "npm install failed"
fi

[[ -f "${FRONTEND_DIR}/.env" ]] || cp "${FRONTEND_DIR}/.env.sample" "${FRONTEND_DIR}/.env"

log "starting the client"
(cd "${FRONTEND_DIR}" && npm run dev >"${REPO_DIR}/.up-frontend.log" 2>&1) &
FRONTEND_PID=$!

for _ in $(seq 1 60); do
  curl -sf http://localhost:5173/ >/dev/null 2>&1 && break
  sleep 0.5
done
curl -sf http://localhost:5173/ >/dev/null 2>&1 \
  || die "the client never answered:$(printf '\n'; tail -20 "${REPO_DIR}/.up-frontend.log")"

# --- ready -------------------------------------------------------------------

cat <<'EOF'

  ┌──────────────────────────────────────────────────────────────┐
  │  Open  http://localhost:5173                                 │
  └──────────────────────────────────────────────────────────────┘

  Sign in — every seeded password is: correct-horse-battery

    Contributor   "Sign in with GitHub" → pick from the list.
                  alice has five scored pull requests and a judgement to read.
                  bob, carol (lapsed) and dave are also there.

    Hirer         sam@tinystudio.example      verified — can search
                  pat@unknown.example         UNVERIFIED — search is refused,
                                              which is the behaviour to look at

    Admin         admin@gitcherrypick.test    the four review queues

  Nothing scores a claim on its own — the model call is still a stub. To judge
  a claim you submitted, drain the queue by hand:

    backend/.dev/evaluator \
      --database-url='postgres://gitcherrypick:dev@127.0.0.1:55434/gitcherrypick_dev?sslmode=disable' \
      --anthropic-api-url='http://127.0.0.1:18082/anthropic' --drain

  Logs: .up-backend.log  .up-frontend.log
  Ctrl-C stops both. The database keeps running.

EOF

log "ready"
wait "${FRONTEND_PID}"
