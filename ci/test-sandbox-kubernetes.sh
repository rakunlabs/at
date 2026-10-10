#!/usr/bin/env bash
# Creates only a disposable kind cluster with an isolated kubeconfig. Never
# applies objects to the caller's current kubectl context. Invoke with bash.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/at-sandbox-kind.XXXXXX")
cluster="at-sandbox-test-${RANDOM}-$$"
created=false
cleanup() {
  status=$?
  if [[ "$created" == true ]]; then
    if [[ "$status" != 0 ]]; then
      kubectl --kubeconfig "$work/kubeconfig" get pods,pvc -A -o wide || true
      kubectl --kubeconfig "$work/kubeconfig" get events -A --sort-by=.lastTimestamp || true
    fi
    if ! kind delete cluster --name "$cluster"; then
      echo "Could not remove test cluster $cluster; operator cleanup required." >&2
      status=1
    fi
  fi
  # This directory was minted by this script, never a user-supplied path.
  rm -rf -- "$work"
  return "$status"
}
trap cleanup EXIT

helper="at-sandbox-helper:kind-test"
image="${AT_TEST_KUBERNETES_IMAGE:-debian:13.7-slim}"
node="${AT_TEST_KIND_NODE_IMAGE:-kindest/node@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f}"
docker build -f "$root/ci/Dockerfile.sandbox-helper" -t "$helper" "$root"
docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image"
kind create cluster --name "$cluster" --image "$node" \
  --config "$root/deploy/kubernetes/kind-test.yaml" --kubeconfig "$work/kubeconfig" --wait 120s
created=true
kind load docker-image --name "$cluster" "$helper" "$image"

cd "$root"
AT_TEST_KUBERNETES_KUBECONFIG="$work/kubeconfig" \
AT_TEST_KUBERNETES_HELPER_IMAGE="$helper" \
AT_TEST_KUBERNETES_IMAGE="$image" \
go test -race -count=1 -timeout=8m -v ./internal/service/sandboxkube -run '^TestKubernetesCluster'
