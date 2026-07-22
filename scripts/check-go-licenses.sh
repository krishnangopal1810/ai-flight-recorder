#!/usr/bin/env bash
set -euo pipefail

# go-licenses v1.6.0 fails to classify this module even though the downloaded
# archive contains a standard BSD-3-Clause LICENSE. Keep the exception narrow:
# a version or license-content change fails until it is reviewed again.
readonly MATHUTIL_MODULE="modernc.org/mathutil"
readonly MATHUTIL_VERSION="v1.7.1"
readonly MATHUTIL_LICENSE_GIT_HASH="128a1b64a48e5ea03d9006ef189997c3e8f81938"

dependency_version="$(go list -m -f '{{.Version}}' "${MATHUTIL_MODULE}")"
dependency_dir="$(go list -m -f '{{.Dir}}' "${MATHUTIL_MODULE}")"
license_file="${dependency_dir}/LICENSE"

if [[ "${dependency_version}" != "${MATHUTIL_VERSION}" ]]; then
  echo "license-audit: ${MATHUTIL_MODULE} changed from reviewed ${MATHUTIL_VERSION} to ${dependency_version}" >&2
  exit 1
fi

if [[ ! -f "${license_file}" ]]; then
  echo "license-audit: ${MATHUTIL_MODULE} is missing its reviewed LICENSE file" >&2
  exit 1
fi

license_hash="$(git hash-object "${license_file}")"
if [[ "${license_hash}" != "${MATHUTIL_LICENSE_GIT_HASH}" ]]; then
  echo "license-audit: ${MATHUTIL_MODULE} LICENSE content changed; review the exception" >&2
  exit 1
fi

go run github.com/google/go-licenses@v1.6.0 check ./... \
  --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0 \
  --ignore="${MATHUTIL_MODULE}"

echo "license-audit: allowed licenses passed; ${MATHUTIL_MODULE}@${MATHUTIL_VERSION} matched its reviewed BSD-3-Clause license"
