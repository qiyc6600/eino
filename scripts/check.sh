#!/usr/bin/env bash
#
# Project checks — the single source of truth for both local runs and CI.
#
# Usage:
#   scripts/check.sh            # everything
#   scripts/check.sh go         # formatting, vet, build, tests
#   scripts/check.sh frontend   # embedded JS syntax
#
# The frontend step exists because `go build` does NOT validate the JavaScript
# pulled in by go:embed. A syntax error there leaves every onclick handler
# undefined — the page stops working entirely — while all Go tests still pass.
set -euo pipefail

cd "$(dirname "$0")/.."

target="${1:-all}"
case "$target" in
  all|go|frontend) ;;
  *) echo "usage: $0 [all|go|frontend]" >&2; exit 2 ;;
esac

step() { printf '\n==> %s\n' "$1"; }
fail() { printf '\nFAIL: %s\n' "$1" >&2; exit 1; }

run_go_checks() {
  command -v go >/dev/null 2>&1 || fail "go is not on PATH"

  step "gofmt (formatting)"
  local unformatted
  unformatted="$(gofmt -l cmd internal integration_test)"
  if [ -n "$unformatted" ]; then
    fail "these files need gofmt -w:
$unformatted"
  fi
  echo "all files formatted"

  step "go vet"
  go vet ./...

  step "go build"
  go build ./...

  step "go test -race"
  # -count=1 defeats the test cache so a green run reflects the current tree.
  go test -race -count=1 ./...
}

run_frontend_checks() {
  # Allow an explicit opt-out for Go-only environments. It must be explicit:
  # silently skipping is how a broken page shipped with green tests.
  if [ "${SKIP_FRONTEND_CHECK:-0}" = "1" ]; then
    echo "skipped (SKIP_FRONTEND_CHECK=1)"
    return
  fi
  command -v node >/dev/null 2>&1 || fail "node is not on PATH, but the frontend syntax check needs it.
Install Node.js, or set SKIP_FRONTEND_CHECK=1 to skip this check explicitly."

  step "frontend syntax (node --check)"
  node --check web/assets/app.js
  echo "web/assets/app.js parses"
}

if [ "$target" = "all" ] || [ "$target" = "go" ]; then
  run_go_checks
fi
if [ "$target" = "all" ] || [ "$target" = "frontend" ]; then
  run_frontend_checks
fi

printf '\nAll requested checks passed (%s).\n' "$target"
