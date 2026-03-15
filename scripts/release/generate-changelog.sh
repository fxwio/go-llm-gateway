#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:?version required}"
DATE="$(date -u +%F)"
BASE_TAG="$(git describe --tags --abbrev=0 2>/dev/null || echo '')"

echo "## [${VERSION}] - ${DATE}"
if [[ -n "${BASE_TAG}" ]]; then
  git log --pretty='- %s (%h)' "${BASE_TAG}"..HEAD
else
  git log --pretty='- %s (%h)' -20
fi
