#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "$ROOT"
dc() { docker compose -f "$ROOT/compose.yml" "$@"; }
pull_repo() (
  cd "$1"
  [[ $(git branch --show-current) == main ]] || { echo "Expected main: $1"; exit 1; }
  git fetch origin main
  git merge-base --is-ancestor HEAD origin/main
  if ! git diff --quiet || ! git diff --cached --quiet || [[ -n $(git ls-files --others --exclude-standard) ]]; then
    # First publication: files deployed before the owner pushes may already
    # exactly match origin/main. Verify every published file before aligning
    # Git metadata; never overwrite the working files or stash user changes.
    sync_dir=$(mktemp -d)
    trap 'rm -f "$sync_dir/index" "$sync_dir/index.lock"; rmdir "$sync_dir"' EXIT
    GIT_INDEX_FILE="$sync_dir/index" git read-tree origin/main
    GIT_INDEX_FILE="$sync_dir/index" git diff --quiet || {
      echo "Local files differ from origin/main in $1. Commit/push the matching local configuration first."; exit 1;
    }
    while IFS= read -r -d '' file; do
      [[ ! -e "$file" ]] || { echo "Upstream deleted file still exists: $file"; exit 1; }
    done < <(git diff --name-only -z --diff-filter=D HEAD origin/main)
    git reset --mixed origin/main
  else
    git merge --ff-only origin/main
  fi
)
case "${1:-status}" in
  status) dc ps ;;
  logs) dc logs --tail 100 "${2:-backend}" ;;
  update)
    for repo in "$ROOT" "$ROOT/../zonenan-admin" "$ROOT/../zonenan-merchant"; do
      pull_repo "$repo"
    done
    exec bash "$ROOT/manage.sh" apply
    ;;
  apply)
    dc config --quiet
    # Pin running images before a build replaces their current tags.
    stamp=$(date +%Y%m%d-%H%M%S)
    for pair in backend:zonenan-backend-new admin:zonenan-admin merchant:zonenan-merchant; do
      kind=${pair%%:*}; container=${pair#*:}
      docker tag "$(docker inspect -f '{{.Image}}' "$container")" "zonenan/$kind:rollback-$stamp"
    done
    echo "Rollback tag: rollback-$stamp"
    # Build before touching running containers.
    dc build backend admin merchant
    # This release has no schema migration. Future schema changes need a versioned migration procedure.
    dc up -d
    curl --fail --retry 20 --retry-connrefused --retry-delay 1 http://127.0.0.1:18088/healthz
    for container in zonenan-admin zonenan-merchant zonenan-nginx; do
      docker exec "$container" nginx -t
      docker exec "$container" nginx -s reload
    done
    dc ps
    ;;
  rollback)
    tag=${2:?Pass the printed rollback tag, e.g. rollback-20260927-220000}
    [[ $tag =~ ^rollback-[0-9]{8}-[0-9]{6}$ ]] || exit 2
    for kind in backend admin merchant; do docker image inspect "zonenan/$kind:$tag" >/dev/null; done
    for kind in backend admin merchant; do docker tag "zonenan/$kind:$tag" "zonenan/$kind:current"; done
    dc up -d
    curl --fail --retry 20 --retry-connrefused --retry-delay 1 http://127.0.0.1:18088/healthz
    for container in zonenan-admin zonenan-merchant zonenan-nginx; do docker exec "$container" nginx -s reload; done
    ;;
  *) echo 'Usage: bash manage.sh {status|logs [service]|update|apply|rollback TAG}'; exit 2 ;;
esac
