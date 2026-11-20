#!/usr/bin/env bash
# Import companies/tasks/findings (and optional data/) from a previous ARTEX instance.
#
# Expects a dump produced by scripts/backup.sh (or plain pg_dump SQL/gz).
#
# Usage:
#   scripts/import-artex.sh --db backups/artex-db-YYYYMMDD.sql.gz
#   scripts/import-artex.sh --db dump.sql.gz --data backups/artex-data-YYYYMMDD.tar.gz --keys /old/host/keys
#
# --keys DIR copies the old instance's secret.key / jwt.key into ./keys (backing
# up the current ones). Required when the dump holds encrypted secrets.
#
# WARNING: This replaces the current Postgres database contents for POSTGRES_DB.
# Stop writers first; compose stack must be running (postgres healthy).
set -euo pipefail
cd "$(dirname "$0")/.."

DB_DUMP=""
DATA_TAR=""
KEYS_DIR=""
YES=0

usage() {
  sed -n '2,14p' "$0" | sed 's/^# \?//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --db) DB_DUMP="${2:?}"; shift 2 ;;
    --data) DATA_TAR="${2:?}"; shift 2 ;;
    --keys) KEYS_DIR="${2:?}"; shift 2 ;;
    -y|--yes) YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$DB_DUMP" ]; then
  echo "--db path is required" >&2
  usage >&2
  exit 2
fi
if [ ! -f "$DB_DUMP" ]; then
  echo "db dump not found: $DB_DUMP" >&2
  exit 1
fi

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi
PGUSER_NAME="${POSTGRES_USER:-artex}"
PGDATABASE_NAME="${POSTGRES_DB:-artex}"

# Target the running stack: local override stack (arkex-local) when present,
# else the plain compose project.
COMPOSE_FILES=(-f docker-compose.yml)
if docker compose -f docker-compose.yml -f docker-compose.local.yml ps >/dev/null 2>&1 \
  && COMPOSE_PROJECT_NAME=arkex-local docker compose -f docker-compose.yml -f docker-compose.local.yml ps --status running 2>/dev/null | grep -q postgres; then
  export COMPOSE_PROJECT_NAME=arkex-local
  COMPOSE_FILES+=(-f docker-compose.local.yml)
fi
compose() { docker compose "${COMPOSE_FILES[@]}" "$@"; }

if [ "$YES" -ne 1 ]; then
  echo "This will DROP and recreate database '$PGDATABASE_NAME', then load $DB_DUMP."
  printf "Type YES to continue: "
  read -r ans
  [ "$ans" = "YES" ] || { echo "aborted"; exit 1; }
fi

echo "recreating database $PGDATABASE_NAME"
compose exec -T postgres psql -U "$PGUSER_NAME" -d postgres -v ON_ERROR_STOP=1 <<SQL
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${PGDATABASE_NAME}' AND pid <> pg_backend_pid();
DROP DATABASE IF EXISTS ${PGDATABASE_NAME};
CREATE DATABASE ${PGDATABASE_NAME} OWNER ${PGUSER_NAME};
SQL

echo "loading dump"
case "$DB_DUMP" in
  *.gz) gunzip -c "$DB_DUMP" | compose exec -T postgres psql -U "$PGUSER_NAME" -d "$PGDATABASE_NAME" -v ON_ERROR_STOP=1 ;;
  *) cat "$DB_DUMP" | compose exec -T postgres psql -U "$PGUSER_NAME" -d "$PGDATABASE_NAME" -v ON_ERROR_STOP=1 ;;
esac

if [ -n "$KEYS_DIR" ]; then
  if [ ! -d "$KEYS_DIR" ]; then
    echo "keys dir not found: $KEYS_DIR" >&2
    exit 1
  fi
  echo "importing keys from $KEYS_DIR into ./keys (existing keys backed up)"
  mkdir -p keys
  for f in secret.key jwt.key; do
    if [ -f "$KEYS_DIR/$f" ]; then
      if [ -f "keys/$f" ]; then
        mv "keys/$f" "keys/$f.bak-$(date -u +%Y%m%dT%H%M%SZ)"
      fi
      cp "$KEYS_DIR/$f" "keys/$f"
      chmod 600 "keys/$f"
    fi
  done
fi

if [ -n "$DATA_TAR" ]; then
  if [ ! -f "$DATA_TAR" ]; then
    echo "data archive not found: $DATA_TAR" >&2
    exit 1
  fi
  echo "restoring data archive into ./data (existing data/ moved aside)"
  TS=$(date -u +%Y%m%dT%H%M%SZ)
  if [ -e data ]; then
    mv data "data.bak-$TS"
  fi
  tar -xzf "$DATA_TAR"
  if [ ! -d data ]; then
    echo "archive did not contain data/" >&2
    exit 1
  fi
fi

echo "import complete — restart the artex service so migrations/secret key reconcile"
echo "  COMPOSE_PROJECT_NAME=arkex-local docker compose -f docker-compose.yml -f docker-compose.local.yml up -d artex"
echo "Bring keys/ from the old instance if secrets were encrypted (ARTEX_SECRET_KEY / keys/secret.key)."
