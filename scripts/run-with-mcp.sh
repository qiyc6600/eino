#!/usr/bin/env bash
# Launch the demo server with one external MCP server enabled.
# Kept as a script so the JSON env var does not have to survive shell quoting.
set -euo pipefail

cd "$(dirname "$0")/.."

export MODEL_PROVIDER=mock
export BOOTSTRAP_ADMIN_USERNAME=admin
export BOOTSTRAP_ADMIN_PASSWORD=verify-admin-1234
export SESSION_STORE=memory
export CHECKPOINT_STORE=memory
export MEMORY_STORE=memory
export THREAD_STORE=memory
export APPROVAL_STORE=memory
export BUSINESS_STORE=memory
export USER_STORE=memory
export ADDR=:8099
export MCP_SERVERS='[{"name":"demo","command":"'"$MCP_SERVER_BIN"'"}]'
export MCP_REQUIRE_APPROVAL=delete_note

exec go run ./cmd/server
