#!/usr/bin/env bash
# Smoke test for a running viewer instance. Pass the base URL, e.g.:
#
#   task run        # in one terminal
#   scripts/smoke.sh http://127.0.0.1:8080
#
set -euo pipefail

if [ "$#" -ge 1 ]; then
	base="$1"
else
	base="http://127.0.0.1:8080"
fi

curl -fsS "$base/healthz" >/dev/null
curl -fsS "$base/" >/dev/null
curl -fsS "$base/op?method=GET&path=/api/player" >/dev/null
curl -fsS "$base/api/spec" >/dev/null
curl -fsS "$base/api/config" >/dev/null
curl -fsS "$base/static/style.css?v=11" >/dev/null

echo "smoke: OK ($base)"
