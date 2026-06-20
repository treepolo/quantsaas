#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = "" ]; then
  echo "Usage: ./deploy/oracle/restore_db.sh backups/quantsaas-YYYYMMDD-HHMMSS.sql[.gz]"
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="${ROOT_DIR}/deploy/oracle/.env.cloud"
COMPOSE_FILE="${ROOT_DIR}/deploy/oracle/docker-compose.oracle.yml"
BACKUP_FILE="$1"

source "${ENV_FILE}"

if [[ "${BACKUP_FILE}" == *.gz ]]; then
  gzip -dc "${BACKUP_FILE}" | docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" exec -T postgres \
    psql -U "${POSTGRES_USER:-quantsaas}" -d "${POSTGRES_DB:-quantsaas}"
else
  cat "${BACKUP_FILE}" | docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" exec -T postgres \
    psql -U "${POSTGRES_USER:-quantsaas}" -d "${POSTGRES_DB:-quantsaas}"
fi

echo "Restored ${BACKUP_FILE}"
