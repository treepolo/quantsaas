#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BACKUP_DIR="${ROOT_DIR}/backups"
ENV_FILE="${ROOT_DIR}/deploy/oracle/.env.cloud"
COMPOSE_FILE="${ROOT_DIR}/deploy/oracle/docker-compose.oracle.yml"

mkdir -p "${BACKUP_DIR}"
source "${ENV_FILE}"

STAMP="$(date -u +%Y%m%d-%H%M%S)"
OUT="${BACKUP_DIR}/quantsaas-${STAMP}.sql.gz"

docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" exec -T postgres \
  pg_dump -U "${POSTGRES_USER:-quantsaas}" -d "${POSTGRES_DB:-quantsaas}" \
  | gzip > "${OUT}"

find "${BACKUP_DIR}" -name "quantsaas-*.sql.gz" -mtime +30 -delete
echo "Wrote ${OUT}"

