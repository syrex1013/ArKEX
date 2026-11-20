#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILE="$(cd "$(dirname "$0")" && pwd)/docker-compose.yml"
ENV_FILE="$(cd "$(dirname "$0")" && pwd)/.env"
ENV_EXAMPLE="$(cd "$(dirname "$0")" && pwd)/.env.example"

# ---- checks ----

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker not found. Install Docker Desktop: https://docs.docker.com/get-docker/" >&2
  exit 1
fi

if ! docker compose version &>/dev/null; then
  echo "ERROR: docker compose (v2) not found. Update Docker Desktop or install the plugin." >&2
  exit 1
fi

if ! docker info &>/dev/null 2>&1; then
  echo "ERROR: Docker daemon not running. Start Docker Desktop and retry." >&2
  exit 1
fi

# ---- env ----

if [ ! -f "$ENV_FILE" ]; then
  cp "$ENV_EXAMPLE" "$ENV_FILE"
  echo ""
  echo "Created .env from .env.example."
  echo "Edit .env to set ANTHROPIC_API_KEY (and optionally POSTGRES_PASSWORD), then re-run this script."
  echo ""
  exit 0
fi

# ---- build & start ----

echo "Building ArKex 2.0..."
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" build

echo "Starting services..."
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d

# ---- wait for health ----

echo "Waiting for ArKex to become healthy..."
PORT=${PORT:-8080}
for i in $(seq 1 30); do
  if curl -sf "http://localhost:${PORT}/api/health" | grep -q '"status":"ok"'; then
    echo ""
    echo "ArKex 2.0 is ready at http://localhost:${PORT}"
    exit 0
  fi
  printf "."
  sleep 2
done

echo ""
echo "WARNING: health check timed out. Check logs with: docker compose logs app"
echo "ArKex may still be starting. Try http://localhost:${PORT} in a moment."
exit 1
