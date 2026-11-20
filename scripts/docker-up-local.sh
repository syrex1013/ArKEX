#!/usr/bin/env bash
# Bring up the ARTEX stack locally with the port-conflict override.
set -euo pipefail
cd /Users/adrian/Projects/arkex
test -f .env || cp .env.example .env
grep -q '^POSTGRES_PASSWORD=.\+' .env || printf 'POSTGRES_PASSWORD=arkex_local_dev\n' >> .env
docker info >/dev/null 2>&1 || {
  echo 'waiting for docker daemon…'
  for i in $(seq 1 60); do
    docker info >/dev/null 2>&1 && break
    sleep 2
  done
}
docker info >/dev/null 2>&1 || { echo 'docker daemon unavailable'; exit 1; }
COMPOSE_PROJECT_NAME=arkex-local docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
COMPOSE_PROJECT_NAME=arkex-local docker compose -f docker-compose.yml -f docker-compose.local.yml ps
curl -sS -m 5 http://127.0.0.1:8797/api/health || true
printf '\n'
