#!/usr/bin/env bash
#
# A running backend to develop a client against.
#
#   backend/scripts/dev.sh              start everything, block until Ctrl-C
#   backend/scripts/dev.sh --reset      rebuild the database from the schema first
#   backend/scripts/dev.sh --rich       seed the scored population as well
#
# What this is NOT: a deployment. Stage 7 owns images and orchestration. This
# starts Postgres in a container and the two binaries on the host, so a client
# developer can point a browser at something and a backend developer can attach
# a debugger without rebuilding an image.
#
# What runs
#
#   postgres            55434   named volume, so state survives a restart
#   fakethirdparty      18082   stands in for GitHub, Google and Resend
#   api                  8080   the thing a client talks to
#
# The ports are deliberately distinct from the suites' (55432 e2e, 55433
# db-test, 18081 e2e's stand-in), so a development database can stay up while
# either suite runs.
#
# Sign-in
#
# There is no real GitHub. `POST /auth/github/start` returns an authorize URL on
# the stand-in, which serves a page listing the seeded cast; picking one
# completes the flow against the real callback. So the whole OAuth path runs —
# state cookie, code exchange, session mint — with nobody needing credentials
# from github.com (ADR-0010).
#
# The evaluator is NOT started. Nothing here judges a claim: a submitted claim
# sits queued until someone runs cmd/evaluator, and stage 5's model call is a
# stub in any case. A client can build every screen except a live score
# appearing on its own.

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
REPO_DIR="$(cd -- "${BACKEND_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.dev.yml"

DB_PORT=55434
FAKE_PORT=18082
API_PORT=8080

DATABASE_URL="postgres://gitcherrypick:dev@127.0.0.1:${DB_PORT}/gitcherrypick_dev?sslmode=disable"
CONTROL_URL="http://127.0.0.1:${FAKE_PORT}"
API_URL="http://127.0.0.1:${API_PORT}"

# Where a browser comes back to after signing in.
#
# The CLIENT, not the API. The API's callback answers JSON, which is the right
# answer for a fetch and a blank stare for a person — so the browser returns to
# the app, and the app calls the API. Override for a different client port.
CLIENT_URL="${GCP_CLIENT_URL:-http://localhost:5173}"

# Matches e2e.SeededPassword. Every seeded account that has a password has this
# one — they are fixtures, not secrets.
SEEDED_PASSWORD="correct-horse-battery"

# Built binaries and the signing key live here and PERSIST, unlike the suites'
# mktemp. A key regenerated on every start would invalidate the session in the
# developer's browser every time they restarted the backend.
STATE_DIR="${BACKEND_DIR}/.dev"

RESET=0
SEEDS="principals,catalogue,repositories"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --reset) RESET=1 ;;
    --rich)  SEEDS="principals,catalogue,repositories,scored_population" ;;
    -h|--help)
      sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror\033[0m %s\n' "$*" >&2; exit 1; }

compose() { docker compose --file "${COMPOSE_FILE}" "$@"; }

FAKE_PID=""
API_PID=""

teardown() {
  local status=$?
  log "stopping"
  [[ -n "${API_PID}"  ]] && kill "${API_PID}"  2>/dev/null || true
  [[ -n "${FAKE_PID}" ]] && kill "${FAKE_PID}" 2>/dev/null || true

  # The database is LEFT RUNNING on purpose. Restarting the script is something
  # a developer does many times an hour, and waiting for Postgres each time is
  # the kind of friction that gets a tool abandoned. Stop it with:
  #   docker compose -f backend/scripts/docker-compose.dev.yml down
  exit "${status}"
}
trap teardown EXIT INT TERM

# --- prerequisites -----------------------------------------------------------

command -v docker >/dev/null || die "docker is not installed"
docker info >/dev/null 2>&1 || die "the docker daemon is not running"

# LibreSSL — macOS's system openssl — has no ed25519. Checked here rather than
# left to fail three steps later with a message that does not say why.
OPENSSL="$(command -v openssl || true)"
[[ -n "${OPENSSL}" ]] || die "openssl is not installed"
if ! "${OPENSSL}" genpkey -algorithm ed25519 -out /dev/null 2>/dev/null; then
  for candidate in /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
    [[ -x "${candidate}" ]] && OPENSSL="${candidate}" && break
  done
fi
"${OPENSSL}" genpkey -algorithm ed25519 -out /dev/null 2>/dev/null \
  || die "this openssl has no ed25519 (macOS ships LibreSSL) — brew install openssl@3"

# --- postgres ----------------------------------------------------------------

log "starting postgres on ${DB_PORT}"
compose up --detach --wait >/dev/null 2>&1 || {
  compose up --detach >/dev/null
  for _ in $(seq 1 60); do
    compose exec -T postgres pg_isready -U gitcherrypick -d gitcherrypick_dev -q && break
    sleep 1
  done
}

# --- binaries and keys -------------------------------------------------------

mkdir -p "${STATE_DIR}"

if [[ ! -f "${STATE_DIR}/signing.pem" ]]; then
  log "generating a signing key (ADR-0011 refuses to boot without one)"
  "${OPENSSL}" genpkey -algorithm ed25519 -out "${STATE_DIR}/signing.pem" 2>/dev/null \
    || die "generating a signing key"
  "${OPENSSL}" pkey -in "${STATE_DIR}/signing.pem" -pubout -out "${STATE_DIR}/signing.pub" 2>/dev/null \
    || die "deriving the public key"
fi

log "building binaries"
cd "${BACKEND_DIR}"
go build -o "${STATE_DIR}/api" ./cmd/api                       || die "building cmd/api"
go build -o "${STATE_DIR}/fakethirdparty" ./cmd/fakethirdparty  || die "building cmd/fakethirdparty"
go build -o "${STATE_DIR}/devdata" ./cmd/devdata                || die "building cmd/devdata"
go build -o "${STATE_DIR}/evaluator" ./cmd/evaluator            || die "building cmd/evaluator"

# --- fake third parties ------------------------------------------------------

log "starting the third-party stand-in on ${FAKE_PORT}"
"${STATE_DIR}/fakethirdparty" \
  --addr="127.0.0.1:${FAKE_PORT}" \
  --self-url="${CONTROL_URL}" \
  --oauth-callback-url="${CLIENT_URL}/auth/github/callback" \
  --hirer-oauth-callback-url="${CLIENT_URL}/auth/hirer/github/callback" \
  >"${STATE_DIR}/fake.log" 2>&1 &
FAKE_PID=$!

for _ in $(seq 1 50); do
  curl -sf "${CONTROL_URL}/_health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "${CONTROL_URL}/_health" >/dev/null 2>&1 \
  || die "the stand-in never answered$(printf '\n'; cat "${STATE_DIR}/fake.log")"

# --- schema and seed ---------------------------------------------------------

# A plain string, not an array. Under `set -u` bash 3.2 — which is what macOS
# ships — expanding an EMPTY array is an unbound-variable error.
RESET_FLAG=""
[[ "${RESET}" == "1" ]] && RESET_FLAG="--reset"

# Always run: it loads the stand-in's identities, which live in memory and are
# gone whenever the stand-in restarts — which is every time this script runs.
log "applying the schema and seeding"
"${STATE_DIR}/devdata" \
  --database-url="${DATABASE_URL}" \
  --rfc-dir="${REPO_DIR}/rfc" \
  --seed-dir="${BACKEND_DIR}/e2e/fixtures/seed" \
  --seed="${SEEDS}" \
  --control-url="${CONTROL_URL}" \
  ${RESET_FLAG} || die "seeding — pass --reset if the schema has changed"

# --- api ---------------------------------------------------------------------

# GitHub: the stand-in by default, github.com when credentials are supplied.
#
# Set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET to sign in against the real
# github.com. The OAuth App must be created by a human (nothing here can mint
# one) and its "Authorization callback URL" must be exactly:
#
#     ${CLIENT_URL}/auth/github/callback
#
# GitHub redirects to whatever is registered on the App — the authorize URL
# sends no redirect_uri — so a mismatch there fails at github.com, not here.
# GITHUB_TOKEN is a separate thing: a PAT for reading PR metadata, unrelated
# to sign-in, and still the stand-in unless you set it.
# The read token is whichever of these is set. It needs no scopes: the adapter
# reads only public repositories, pull requests and their reviews, so the token
# exists to lift the rate limit from 60/hour to 5,000 — not to grant access.
GH_PAT="${GITHUB_TOKEN:-${GITHUB_GITCHERRYPICK_PAT_DEV:-}}"

if [[ -n "${GITHUB_CLIENT_ID:-}" && -n "${GITHUB_CLIENT_SECRET:-}" ]]; then
  log "github: live — client id ${GITHUB_CLIENT_ID:0:6}…, callback ${CLIENT_URL}/auth/github/callback"
  GITHUB_FLAGS=(
    --github-client-id="${GITHUB_CLIENT_ID}"
    --github-client-secret="${GITHUB_CLIENT_SECRET}"
    --github-token="${GH_PAT:-dev}"
  )
  # No --github-api-url / --github-oauth-url: the binary already defaults to
  # api.github.com and github.com.
  if [[ -z "${GH_PAT}" ]]; then
    warn "no PAT (GITHUB_TOKEN or GITHUB_GITCHERRYPICK_PAT_DEV), so PR metadata still comes from the stand-in"
    GITHUB_FLAGS+=(--github-api-url="${CONTROL_URL}/github")
  else
    log "github: PR metadata live too — pat ${GH_PAT:0:11}…"
  fi
else
  GITHUB_FLAGS=(
    --github-api-url="${CONTROL_URL}/github"
    --github-oauth-url="${CONTROL_URL}/github"
    --github-client-id=dev --github-client-secret=dev --github-token=dev
  )
fi

log "starting the api on ${API_PORT}"
"${STATE_DIR}/api" \
  --addr="127.0.0.1:${API_PORT}" \
  --database-url="${DATABASE_URL}" \
  --signing-key="${STATE_DIR}/signing.pem" \
  --verify-key="dev:${STATE_DIR}/signing.pub" \
  "${GITHUB_FLAGS[@]}" \
  --google-oidc-issuer="${CONTROL_URL}/google" \
  --google-client-id=dev --google-client-secret=dev \
  --google-redirect-uri="${CLIENT_URL}/auth/google/callback" \
  --resend-api-url="${CONTROL_URL}/resend" \
  --resend-api-key=dev \
  --app-url="${CLIENT_URL}" \
  --places-api-url="${CONTROL_URL}/places" \
  --money-api-url="${CONTROL_URL}/money" \
  >"${STATE_DIR}/api.log" 2>&1 &
API_PID=$!

for _ in $(seq 1 50); do
  curl -sf "${API_URL}/health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "${API_URL}/health" >/dev/null 2>&1 \
  || die "the api never answered$(printf '\n'; tail -30 "${STATE_DIR}/api.log")"

# --- ready -------------------------------------------------------------------

cat <<EOF

  api                 ${API_URL}
  third-party         ${CONTROL_URL}
  database            ${DATABASE_URL}

  logs                ${STATE_DIR}/api.log
                      ${STATE_DIR}/fake.log

  sign in — contributor
    POST ${API_URL}/auth/github/start, open authorize_url, pick an account.
    Seeded: alice (5 scored PRs), bob, carol (lapsed), dave.

  sign in — hirer         password: ${SEEDED_PASSWORD}
    POST ${API_URL}/auth/hirer/login  {"username": …, "password": …}
      sam   verified      (sam@tinystudio.example)
      pat   UNVERIFIED    (pat@unknown.example) — cannot search or shortlist

    A USERNAME, not an email (ADR-0016). Two seats may share a contact
    address, so an address identifies nobody.

  sign in — admin         password: ${SEEDED_PASSWORD}
    POST ${API_URL}/auth/admin/login  admin@gitcherrypick.test
  drain the queue     ${STATE_DIR}/evaluator --database-url='${DATABASE_URL}' \\
                        --anthropic-api-url='${CONTROL_URL}/anthropic' --drain

  Ctrl-C stops the api and the stand-in. The database keeps running:
  docker compose -f ${COMPOSE_FILE} down

EOF

log "ready — Ctrl-C to stop"
wait "${API_PID}"
