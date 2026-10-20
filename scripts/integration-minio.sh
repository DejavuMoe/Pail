#!/usr/bin/env bash
# Runs the MinIO integration suite (./integration) against a throwaway, isolated
# MinIO container on 127.0.0.1. Linux only; needs docker, curl and go on PATH.
#
# Configuration (environment variables, all optional):
#   PAIL_IT_PORT          host port bound on 127.0.0.1 (1024-65535, default 19000)
#   PAIL_IT_MINIO_IMAGE   'pinned' (default) = build integration/Dockerfile.minio;
#                         'latest' = build PAIL_IT_MINIO_TAG from integration/Dockerfile.minio-latest;
#                         'local:<image>' = use an already loaded image without pulling;
#                         anything else = full image reference, pulled by Docker
#   PAIL_IT_MINIO_TAG     upstream release tag for 'latest' (default RELEASE.2025-10-15T17-29-55Z)
#   PAIL_IT_GOTEST_ARGS   extra go test arguments split on whitespace, e.g. '-run TestApp'
#
# The container gets a unique name and an ownership label; the exit trap removes
# it only when the label matches this run. Data lives on tmpfs; the credentials
# are generated per run and exist only inside this process and the container.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd -- "$script_dir/.."
if [ ! -f go.mod ] || [ ! -f integration/Dockerfile.minio ]; then
    echo "Expected the Pail repository root at $(pwd)" >&2
    exit 2
fi

port="${PAIL_IT_PORT:-19000}"
minio_choice="${PAIL_IT_MINIO_IMAGE:-pinned}"
minio_tag="${PAIL_IT_MINIO_TAG:-RELEASE.2025-10-15T17-29-55Z}"
gotest_args_raw="${PAIL_IT_GOTEST_ARGS:-}"

# Same validation as scripts/test-integration.ps1; the patterns live in variables
# so bash does not reinterpret their brackets.
re_port='^[0-9]{1,5}$'
re_image='^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$'
re_tag='^RELEASE\.[0-9T-]+Z$'
re_args='^[A-Za-z0-9_ .|^$()=/-]*$'
if ! [[ "$port" =~ $re_port ]] || [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
    echo "Invalid PAIL_IT_PORT: $port (expected 1024-65535)" >&2
    exit 2
fi
if ! [[ "$minio_choice" =~ $re_image ]]; then
    echo "Invalid PAIL_IT_MINIO_IMAGE: $minio_choice" >&2
    exit 2
fi
if ! [[ "$minio_tag" =~ $re_tag ]]; then
    echo "Invalid PAIL_IT_MINIO_TAG: $minio_tag" >&2
    exit 2
fi
if ! [[ "$gotest_args_raw" =~ $re_args ]]; then
    echo "Invalid PAIL_IT_GOTEST_ARGS: $gotest_args_raw" >&2
    exit 2
fi
# Split on whitespace only: no globbing, no shell interpretation of | $ ( ).
read -r -a gotest_args <<< "$gotest_args_raw" || true

command -v docker >/dev/null
command -v curl >/dev/null
command -v go >/dev/null
docker info >/dev/null

case "$minio_choice" in
    local:*)
        # CI loads its cached Buildx result; never pull or silently rebuild it.
        image="${minio_choice#local:}"
        docker image inspect "$image" >/dev/null
        ;;
    pinned)
        image='pail-test-minio:0d7408fc9969-go1.24.2'
        docker build --file integration/Dockerfile.minio --tag "$image" integration
        ;;
    latest)
        # Docker Hub / quay.io no longer publish current community images: build the newest release tag from source.
        image="pail-test-minio:latest-$minio_tag"
        docker build --file integration/Dockerfile.minio-latest --build-arg "MINIO_TAG=$minio_tag" --tag "$image" integration
        ;;
    *)
        image="$minio_choice"
        docker pull "$image"
        ;;
esac

run_id="$(date +%s)-$$-$RANDOM"
container="pail-integration-$run_id"
access_key='pail-local-test'
secret_key="pail-test-$RANDOM-$RANDOM-$RANDOM-$RANDOM"

cleanup() {
    status=$?
    trap - EXIT INT TERM
    if docker inspect "$container" >/dev/null 2>&1; then
        owner=$(docker inspect --format '{{index .Config.Labels "com.pail.integration"}}' "$container")
        if [ "$owner" = "$run_id" ]; then
            if ! docker rm --force "$container" >/dev/null; then
                echo "Failed to remove owned integration container: $container" >&2
                status=1
            fi
        else
            echo "Refusing to remove container with unexpected ownership: $container" >&2
            status=1
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run --detach --name "$container" \
    --label "com.pail.integration=$run_id" \
    --publish "127.0.0.1:$port:9000" \
    --tmpfs /data:rw,size=2g \
    --env "MINIO_ROOT_USER=$access_key" \
    --env "MINIO_ROOT_PASSWORD=$secret_key" \
    "$image" server /data --console-address :9001 >/dev/null

ready=0
for _ in $(seq 1 60); do
    if curl --noproxy '*' --silent --fail --max-time 2 "http://127.0.0.1:$port/minio/health/ready" >/dev/null; then
        ready=1
        break
    fi
    if [ "$(docker inspect --format '{{.State.Running}}' "$container")" != 'true' ]; then
        echo "MinIO exited before readiness: $container" >&2
        exit 1
    fi
    sleep 1
done
if [ "$ready" != 1 ]; then
    echo 'MinIO did not become ready within the startup budget.' >&2
    exit 1
fi

if [ "$minio_choice" = pinned ]; then
    pinned_note=' [pinned build: MinIO RELEASE.2025-04-22T22-12-26Z, commit 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e; built without release ldflags, hence the DEVELOPMENT.GOGET banner]'
elif [ "$minio_choice" = latest ]; then
    pinned_note=" [latest build: MinIO $minio_tag built from the upstream tag]"
else
    pinned_note=''
fi
minio_version="$(docker logs "$container" 2>&1 | grep -m1 -i 'Version:' | sed 's/^ *//' || true)"
if [ -z "$minio_version" ]; then
    minio_version="(banner not found) $(curl --noproxy '*' -sI --max-time 2 "http://127.0.0.1:$port/minio/health/live" | tr -d '\r' | grep -i '^server' || true)"
fi
minio_version="$minio_version$pinned_note"
echo "MinIO image: $image ($minio_choice); server $minio_version"
export PAIL_TEST_SERVER_VERSION="$minio_version"
export PAIL_TEST_ENDPOINT="http://127.0.0.1:$port"
export PAIL_TEST_ACCESS_KEY="$access_key"
export PAIL_TEST_SECRET_KEY="$secret_key"
export AWS_EC2_METADATA_DISABLED=true
export NO_PROXY='127.0.0.1,localhost,::1'
export no_proxy="$NO_PROXY"
go test ./integration -count=1 -v -timeout 20m ${gotest_args[@]+"${gotest_args[@]}"}
