#!/usr/bin/env bash
# Builds the service images for Fargate (linux/arm64) and pushes them to
# their ECR repositories, tagged with the current commit's short SHA.
#
# Usage, from anywhere:
#   scripts/push-image.sh              # worker and api
#   scripts/push-image.sh worker       # one service
#
# A service's directory must have no uncommitted changes, so the tag names
# the code that is in the image. ECR tags are immutable, so a tag that is
# already in the repository is skipped instead of pushed again.
#
# Needs docker (running), aws, jq and terraform (with infra/ initialised
# against the state bucket). Repository URLs and the region come from the
# Terraform outputs.
#
# Environment overrides:
#   PLATFORM  image platform (default linux/arm64, the Fargate tasks' platform)

set -euo pipefail

PLATFORM="${PLATFORM:-linux/arm64}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Each service's build context.
service_dir() {
  case "$1" in
    worker) echo worker ;;
    api) echo server ;;
    *) echo "error: unknown service $1 (expected worker or api)" >&2; exit 1 ;;
  esac
}

for cmd in docker aws jq terraform git; do
  command -v "$cmd" >/dev/null || { echo "error: $cmd is not installed" >&2; exit 1; }
done
docker info >/dev/null 2>&1 || { echo "error: docker is not running (start Docker Desktop)" >&2; exit 1; }

SERVICES=("$@")
[[ ${#SERVICES[@]} -gt 0 ]] || SERVICES=(worker api)

TAG="$(git -C "$ROOT" rev-parse --short HEAD)"
for svc in "${SERVICES[@]}"; do
  dir="$(service_dir "$svc")"
  if [[ -n "$(git -C "$ROOT" status --porcelain -- "$dir")" ]]; then
    echo "error: $dir/ has uncommitted changes; commit them so tag $TAG matches the image" >&2
    exit 1
  fi
done

echo "Reading Terraform outputs..."
REGION="$(terraform -chdir="$ROOT/infra" output -raw aws_region)"
URLS="$(terraform -chdir="$ROOT/infra" output -json ecr_repository_urls)"

# All repositories share one registry: <account>.dkr.ecr.<region>.amazonaws.com
REGISTRY="$(jq -r '.worker' <<<"$URLS" | cut -d/ -f1)"
echo "Logging in to $REGISTRY..."
aws ecr get-login-password --region "$REGION" |
  docker login --username AWS --password-stdin "$REGISTRY" >/dev/null

for svc in "${SERVICES[@]}"; do
  dir="$(service_dir "$svc")"
  repo_url="$(jq -r --arg s "$svc" '.[$s] // empty' <<<"$URLS")"
  [[ -n "$repo_url" ]] || { echo "error: no ECR repository for $svc in the Terraform outputs" >&2; exit 1; }
  repo_name="${repo_url#*/}"

  echo
  if aws ecr describe-images --region "$REGION" --repository-name "$repo_name" \
      --image-ids imageTag="$TAG" >/dev/null 2>&1; then
    echo "$svc: $repo_url:$TAG is already in ECR, skipping"
    continue
  fi

  echo "$svc: building $repo_url:$TAG ($PLATFORM)..."
  docker build --platform "$PLATFORM" -t "$repo_url:$TAG" "$ROOT/$dir"
  echo "$svc: pushing..."
  docker push "$repo_url:$TAG"
done

echo
echo "Done. To run this tag:"
echo "  terraform -chdir=infra apply -var worker_image_tag=$TAG -var worker_desired_count=1"
