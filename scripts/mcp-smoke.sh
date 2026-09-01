#!/usr/bin/env bash
# Smoke-test the MCP server: handshake + one tool call, printed as JSON.
#
#   scripts/mcp-smoke.sh                                    # on_duty, today, all duties
#   scripts/mcp-smoke.sh list_duties
#   scripts/mcp-smoke.sh on_duty     '{"duty":"hall"}'
#   scripts/mcp-smoke.sh upcoming    '{"duty":"floor","weeks":2}'
set -uo pipefail

tool="${1:-on_duty}"
args='{}'
[ "$#" -ge 2 ] && args="$2"

cd "$(dirname "$0")/.."

init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
inited='{"jsonrpc":"2.0","method":"notifications/initialized"}'
call=$(jq -cn --arg t "$tool" --argjson a "$args" \
  '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:$t,arguments:$a}}')

{ printf '%s\n%s\n%s\n' "$init" "$inited" "$call"; sleep 2; } \
  | task mcp 2>/dev/null \
  | grep '"id":2' \
  | jq '.result.structuredContent // .result'
