#!/usr/bin/env bash
set -euo pipefail

release_tag="${1:?usage: build-release-assets.sh <vX.Y.Z[-prerelease]> [dist-dir]}"
release_dir="${2:-dist}"
release_version="${release_tag#v}"
if [[ ! "${release_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
  echo "invalid release tag: ${release_tag}" >&2
  exit 2
fi

python_version="${release_version}"
if [[ "${release_version}" =~ ^([0-9]+\.[0-9]+\.[0-9]+)-alpha\.([0-9]+)$ ]]; then
  python_version="${BASH_REMATCH[1]}a${BASH_REMATCH[2]}"
elif [[ "${release_version}" =~ ^([0-9]+\.[0-9]+\.[0-9]+)-beta\.([0-9]+)$ ]]; then
  python_version="${BASH_REMATCH[1]}b${BASH_REMATCH[2]}"
elif [[ "${release_version}" =~ ^([0-9]+\.[0-9]+\.[0-9]+)-rc\.([0-9]+)$ ]]; then
  python_version="${BASH_REMATCH[1]}rc${BASH_REMATCH[2]}"
fi

staging_dir="$(mktemp -d)"
trap 'rm -rf "${staging_dir}"' EXIT
mkdir -p "${release_dir}" "${staging_dir}/python" "${staging_dir}/typescript"
cp -R sdk/python/. "${staging_dir}/python/"
cp -R sdk/typescript/. "${staging_dir}/typescript/"
rm -rf "${staging_dir}/python/.venv" "${staging_dir}/typescript/node_modules" "${staging_dir}/typescript/dist"

python3 - "${staging_dir}/python/pyproject.toml" "${python_version}" <<'PY'
from pathlib import Path
import re
import sys

path = Path(sys.argv[1])
version = sys.argv[2]
text = path.read_text()
updated, count = re.subn(r'(?m)^version = "[^"]+"$', f'version = "{version}"', text, count=1)
if count != 1:
    raise SystemExit("python package version field was not found")
path.write_text(updated)
PY

npm --prefix "${staging_dir}/typescript" version "${release_version}" --no-git-tag-version --allow-same-version >/dev/null
uv build "${staging_dir}/python" --out-dir "${release_dir}"
npm --prefix "${staging_dir}/typescript" ci
npm --prefix "${staging_dir}/typescript" run build
npm pack "${staging_dir}/typescript" --pack-destination "$(cd "${release_dir}" && pwd)" >/dev/null

wheel="$(find "${release_dir}" -maxdepth 1 -type f -name "ai_flight_recorder-${python_version}-*.whl" -print -quit)"
sdist="$(find "${release_dir}" -maxdepth 1 -type f -name "ai_flight_recorder-${python_version}.tar.gz" -print -quit)"
typescript_package="$(find "${release_dir}" -maxdepth 1 -type f -name "ai-flight-recorder-sdk-${release_version}.tgz" -print -quit)"
test -n "${wheel}" -a -n "${sdist}" -a -n "${typescript_package}"
test -s "${release_dir}/checksums.txt"
cli_artifacts=()
for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  cli_archive="${release_dir}/ai-flight-recorder_${release_version}_${platform}.tar.gz"
  if [[ ! -s "${cli_archive}" ]]; then
    echo "missing CLI release archive: ${cli_archive}" >&2
    exit 1
  fi
  cli_artifacts+=("${cli_archive}")
done

smoke_dir="$(mktemp -d)"
python3 -m venv "${smoke_dir}/venv"
"${smoke_dir}/venv/bin/pip" install --quiet "${wheel}[integrations]"
"${smoke_dir}/venv/bin/python" - "${python_version}" <<'PY'
from importlib.metadata import version
import sys
import flight_recorder
from flight_recorder import flight_recorder_middleware, wrap_chat_model, wrap_openai
from openai import OpenAI
from langchain_core.messages import AIMessage

assert flight_recorder is not None
assert OpenAI is not None and AIMessage is not None
assert wrap_openai is not None and wrap_chat_model is not None and flight_recorder_middleware is not None
assert version("ai-flight-recorder") == sys.argv[1]
PY
mkdir -p "${smoke_dir}/node"
npm --prefix "${smoke_dir}/node" init --yes >/dev/null
npm --prefix "${smoke_dir}/node" install --quiet "${typescript_package}" openai@6.48.0 @langchain/core@1.2.3
node --input-type=module - "${smoke_dir}/node" "${release_version}" <<'JS'
import {readFileSync} from "node:fs";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = process.argv[2];
const expected = process.argv[3];
const metadata = JSON.parse(readFileSync(resolve(root, "node_modules/@ai-flight-recorder/sdk/package.json"), "utf8"));
if (metadata.version !== expected) throw new Error(`TypeScript SDK version ${metadata.version} != ${expected}`);
await import(pathToFileURL(resolve(root, "node_modules/@ai-flight-recorder/sdk/dist/index.js")));
await import(pathToFileURL(resolve(root, "node_modules/@ai-flight-recorder/sdk/dist/openai.js")));
await import(pathToFileURL(resolve(root, "node_modules/@ai-flight-recorder/sdk/dist/langchain.js")));
await import(pathToFileURL(resolve(root, "node_modules/@ai-flight-recorder/sdk/dist/telemetry.js")));
JS
rm -rf "${smoke_dir}"

for artifact in "${cli_artifacts[@]}" "${wheel}" "${sdist}" "${typescript_package}"; do
  syft "file:${artifact}" -o cyclonedx-json="${artifact}.sbom.json"
done

python3 - "${release_dir}" "${release_tag}" "${release_version}" "${python_version}" <<'PY'
from hashlib import sha256
from pathlib import Path
import json
import sys

directory = Path(sys.argv[1])
tag, version, python_version = sys.argv[2:]
assets = []
for path in sorted(directory.iterdir()):
    if not path.is_file() or path.name in {"release-checksums.txt", "release-manifest.json"} or path.name.endswith(".sigstore.json"):
        continue
    if path.name.startswith("ai-flight-recorder_") and path.name.endswith(".tar.gz"):
        kind = "cli"
    elif path.name.startswith("ai_flight_recorder-") and path.suffix in {".whl", ".gz"}:
        kind = "python_sdk"
    elif path.name.startswith("ai-flight-recorder-sdk-") and path.suffix == ".tgz":
        kind = "typescript_sdk"
    elif ".sbom" in path.name:
        kind = "sbom"
    elif path.name == "checksums.txt":
        kind = "checksum"
    else:
        continue
    assets.append({"name": path.name, "kind": kind, "sha256": sha256(path.read_bytes()).hexdigest(), "size": path.stat().st_size})
manifest = {
    "schema_version": "1",
    "tag": tag,
    "release_version": version,
    "packages": {"cli": version, "python": version, "typescript": version},
    "artifact_versions": {"cli": version, "python": python_version, "typescript": version},
    "assets": assets,
}
manifest_path = directory / "release-manifest.json"
manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
checksummed = [directory / asset["name"] for asset in assets] + [manifest_path]
(directory / "release-checksums.txt").write_text("".join(
    f"{sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in checksummed
))
PY

echo "release assets: cli=${release_version} python=${python_version} typescript=${release_version}"
