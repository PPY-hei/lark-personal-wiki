#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p tmp bin

if [[ -f tmp/server.pid ]]; then
  pid="$(cat tmp/server.pid)"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    echo "server already running: pid=$pid"
    echo "logs: tail -f tmp/server.log"
    exit 0
  fi
fi

go build -o bin/feishu-kb-server ./cmd/server
nohup ./bin/feishu-kb-server >> tmp/server.log 2>&1 &
echo "$!" > tmp/server.pid
echo "server started: pid=$(cat tmp/server.pid)"
echo "logs: tail -f tmp/server.log"
