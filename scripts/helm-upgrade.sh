#!/usr/bin/env bash
set -euo pipefail

RELEASE_NAME="${1:?release name required}"
NAMESPACE="${2:?namespace required}"
CHART_PATH="${3:?chart path required}"
VALUES_FILE="${4:?values file required}"
IMAGE_REPOSITORY="${5:?image repository required}"
IMAGE_TAG="${6:?image tag required}"

helm upgrade --install "${RELEASE_NAME}" "${CHART_PATH}" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --wait \
  --atomic \
  --timeout 5m \
  -f "${VALUES_FILE}" \
  --set image.repository="${IMAGE_REPOSITORY}" \
  --set image.tag="${IMAGE_TAG}"
