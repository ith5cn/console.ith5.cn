#!/usr/bin/env bash

set -Eeuo pipefail

readonly APP_DIR="/opt/ith5-console/src"
readonly COMPOSE_DIR="${APP_DIR}/deploy/ith5cn"
readonly IMAGE_NAME="ith5-console-app"
readonly CONTAINER_NAME="ith5-console-app-1"
readonly HEALTH_URL="http://127.0.0.1:18080/healthz"
readonly LOCK_FILE="/tmp/ith5-console-deploy.lock"

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  echo "Another console deployment is already running."
  exit 1
fi

cd "${COMPOSE_DIR}"
test -s .env
docker compose config >/dev/null

old_image="$(docker image inspect "${IMAGE_NAME}" --format '{{.Id}}' 2>/dev/null || true)"
version="${1:-$(date -u +%Y%m%d-%H%M%S)}"

echo "Building console image: ${version}"
APP_VERSION="${version}" docker compose build --pull app

echo "Starting console container"
APP_VERSION="${version}" docker compose up -d --no-deps --remove-orphans app

healthy=false
for _ in $(seq 1 45); do
  if curl --fail --silent --show-error --max-time 3 "${HEALTH_URL}" >/dev/null; then
    healthy=true
    break
  fi
  sleep 2
done

if [[ "${healthy}" != "true" ]]; then
  echo "Health check failed; restoring the previous image."
  docker logs --tail 120 "${CONTAINER_NAME}" || true
  if [[ -n "${old_image}" ]]; then
    docker tag "${old_image}" "${IMAGE_NAME}:latest"
    docker compose up -d --no-deps --force-recreate --no-build app
  fi
  exit 1
fi

docker image prune --force --filter 'until=168h' >/dev/null
echo "Deployment completed and health check passed."
