#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ ! -f tmp/server.pid ]]; then
  echo "tmp/server.pid not found"
  exit 0
fi

pid="$(cat tmp/server.pid)"
if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
  echo "server is not running"
  rm -f tmp/server.pid
  exit 0
fi

kill "$pid"
rm -f tmp/server.pid
echo "server stopped: pid=$pid"
