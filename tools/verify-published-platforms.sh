#!/bin/sh
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

set -eu
registry="${REGISTRY:?REGISTRY is required}"
version="${VERSION:?VERSION is required}"
latest_tag="${LATEST_TAG:-latest}"
platforms="${PLATFORMS:?PLATFORMS is required}"
images="${IMAGES:?IMAGES is required}"

wanted=$(echo "$platforms" | tr ',' ' ')
failed=0

for img in $images; do
  for tag in "$version" "$latest_tag"; do
    ref="$registry/$img:$tag"
    if ! raw=$(docker buildx imagetools inspect --raw "$ref" 2>&1); then
      echo "verify-platforms: cannot read $ref from the registry: $raw"
      failed=$((failed + 1))
      continue
    fi
    published=$(echo "$raw" | tr -d ' \n' | grep -o '"platform":{[^}]*}' \
      | sed -e 's/.*"architecture":"\([^"]*\)".*"os":"\([^"]*\)".*/\2\/\1/' \
            -e 's/.*"os":"\([^"]*\)".*"architecture":"\([^"]*\)".*/\1\/\2/' \
      | grep -v '^unknown/' | sort -u)
    for want in $wanted; do
      if ! echo "$published" | grep -qx "$want"; then
        echo "verify-platforms: $ref does not publish $want — it carries: $(echo "$published" | tr '\n' ' ')"
        failed=$((failed + 1))
      fi
    done
  done
done

if [ "$failed" -gt 0 ]; then
  echo "verify-platforms: $failed published tag/platform check(s) failed; the release publishes $platforms on every tag"
  exit 1
fi

echo "verify-platforms: every pushed tag publishes $platforms"
