#!/usr/bin/env bash
# GitCherryPick cold-machine bootstrap.
#
# Idempotent: safe to re-run. Installs what it can, reports what it can't.
# Exits non-zero only on a HARD failure (missing language toolchain).
# Soft gaps (Docker down, secrets unset) are reported, not fatal.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

if [ -t 1 ]; then
  R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; B=$'\033[1m'; X=$'\033[0m'
else
  R=''; G=''; Y=''; B=''; X=''
fi

ok()   { printf '  %s✓%s %s\n' "$G" "$X" "$*"; }
warn() { printf '  %s!%s %s\n' "$Y" "$X" "$*"; }
bad()  { printf '  %s✗%s %s\n' "$R" "$X" "$*"; }
hdr()  { printf '\n%s%s%s\n' "$B" "$*" "$X"; }

HARD_FAIL=0
SOFT_GAPS=()

# --- language toolchains (hard requirements) --------------------------------
hdr "Toolchain"

# Compare dotted versions: verlte 1.22 1.24.4 -> true
verlte() { [ "$1" = "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" ]; }

GO_MIN=1.22
NODE_MIN=20

if command -v go >/dev/null 2>&1; then
  GO_VER="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  if verlte "$GO_MIN" "$GO_VER"; then
    ok "go $GO_VER (>= $GO_MIN)"
  else
    bad "go $GO_VER is older than the required $GO_MIN"; HARD_FAIL=1
  fi
else
  bad "go not found — install Go >= $GO_MIN (https://go.dev/dl/)"; HARD_FAIL=1
fi

if command -v node >/dev/null 2>&1; then
  NODE_VER="$(node --version | sed 's/^v//')"
  if verlte "$NODE_MIN" "$NODE_VER"; then
    ok "node $NODE_VER (>= $NODE_MIN)"
  else
    bad "node $NODE_VER is older than the required $NODE_MIN"; HARD_FAIL=1
  fi
else
  bad "node not found — install Node >= $NODE_MIN"; HARD_FAIL=1
fi

command -v npm  >/dev/null 2>&1 && ok "npm $(npm --version)"  || { bad "npm not found"; HARD_FAIL=1; }
command -v git  >/dev/null 2>&1 && ok "git $(git --version | awk '{print $3}')" || { bad "git not found"; HARD_FAIL=1; }

if [ "$HARD_FAIL" -ne 0 ]; then
  hdr "Result"
  bad "Missing a required toolchain. Install the items marked ✗ above, then re-run."
  exit 1
fi

# --- go dev tools (installed on demand) -------------------------------------
hdr "Go dev tools"

GOBIN="$(go env GOPATH)/bin"
export PATH="$GOBIN:$PATH"

install_go_tool() {
  local bin="$1" pkg="$2"
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$bin (already installed)"
    return 0
  fi
  printf '  … installing %s\n' "$bin"
  if go install "$pkg" >/dev/null 2>&1; then
    ok "$bin installed to $GOBIN"
  else
    warn "could not install $bin ($pkg) — check network access"
    SOFT_GAPS+=("go tool '$bin' is not installed")
  fi
}

install_go_tool migrate        "github.com/golang-migrate/migrate/v4/cmd/migrate@latest"
install_go_tool mockery        "github.com/vektra/mockery/v2@latest"
install_go_tool golangci-lint  "github.com/golangci/golangci-lint/cmd/golangci-lint@latest"

case ":$PATH:" in
  *":$GOBIN:"*) ;;
  *) warn "$GOBIN is not on your PATH — add it to your shell profile" ;;
esac

# --- docker ------------------------------------------------------------------
hdr "Docker"

if command -v docker >/dev/null 2>&1; then
  ok "docker $(docker --version | awk '{print $3}' | tr -d ,)"
  if docker compose version >/dev/null 2>&1; then
    ok "docker compose $(docker compose version --short 2>/dev/null)"
  else
    warn "docker compose plugin not available"
    SOFT_GAPS+=("docker compose plugin missing")
  fi
  if docker info >/dev/null 2>&1; then
    ok "docker daemon is running"
  else
    warn "docker daemon is NOT running — start Docker Desktop"
    SOFT_GAPS+=("docker daemon down — blocks integration tests (stage 3) and everything after")
  fi
else
  warn "docker not found — required from stage 3 onward"
  SOFT_GAPS+=("docker not installed")
fi

# --- dependencies ------------------------------------------------------------
hdr "Dependencies"

if [ -f backend/go.mod ]; then
  if (cd backend && go mod download) >/dev/null 2>&1; then
    ok "backend go modules downloaded"
  else
    warn "backend 'go mod download' failed"
    SOFT_GAPS+=("backend go modules not downloaded")
  fi
else
  ok "backend/go.mod not present yet (stage 4 has not started) — skipping"
fi

if [ -f frontend/package.json ]; then
  if (cd frontend && npm install --no-audit --no-fund) >/dev/null 2>&1; then
    ok "frontend npm packages installed"
  else
    warn "frontend 'npm install' failed"
    SOFT_GAPS+=("frontend npm packages not installed")
  fi
else
  ok "frontend/package.json not present yet (stage 6 has not started) — skipping"
fi

# --- env files ---------------------------------------------------------------
hdr "Environment files"

seed_env() {
  local dir="$1"
  if [ -f "$dir/.env.sample" ]; then
    if [ -f "$dir/.env" ]; then
      ok "$dir/.env exists"
    else
      cp "$dir/.env.sample" "$dir/.env"
      ok "created $dir/.env from .env.sample — fill in the blanks"
    fi
  fi
}
seed_env backend
seed_env frontend
[ -f backend/.env.sample ] || [ -f frontend/.env.sample ] && : || ok "no .env.sample files yet — nothing to seed"

# --- secrets doctor ----------------------------------------------------------
hdr "Secrets (needed only for live runs, not for tests)"

check_secret() {
  local name="$1" why="$2"
  if [ -n "${!name:-}" ]; then
    ok "$name is set"
  else
    warn "$name is unset — $why"
    SOFT_GAPS+=("$name unset — $why")
  fi
}

if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  ok "ANTHROPIC_API_KEY is set"
elif command -v ant >/dev/null 2>&1 && ant auth status >/dev/null 2>&1; then
  ok "ANTHROPIC_API_KEY unset, but an 'ant' auth profile is active (SDK will use it)"
else
  warn "ANTHROPIC_API_KEY unset — evaluator cannot call the model"
  SOFT_GAPS+=("ANTHROPIC_API_KEY unset (or run 'ant auth login')")
fi

check_secret GITHUB_TOKEN         "GitHub API is limited to 60 req/hr unauthenticated"
check_secret GITHUB_CLIENT_ID     "login flow needs a GitHub OAuth App (create at github.com/settings/developers)"
check_secret GITHUB_CLIENT_SECRET "login flow needs a GitHub OAuth App (create at github.com/settings/developers)"

# --- summary -----------------------------------------------------------------
hdr "Result"

if [ ${#SOFT_GAPS[@]} -eq 0 ]; then
  ok "Everything is ready."
else
  printf '  Ready to work, with %d open gap(s):\n\n' "${#SOFT_GAPS[@]}"
  for g in "${SOFT_GAPS[@]}"; do printf '    %s-%s %s\n' "$Y" "$X" "$g"; done
  printf '\n  Unit tests and planning work fine with these open.\n'
  printf '  Integration tests (stage 3) need the Docker daemon.\n'
  printf '  The secrets are only needed to run against live GitHub / live Claude.\n'
fi
echo
exit 0
