#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
version="${1:-0.1.0-dev}"
revision="${2:-unknown}"
if [[ ! "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
  echo 'Version must be MAJOR.MINOR.PATCH[-prerelease], optionally prefixed with v.' >&2
  exit 2
fi
if [[ ! "$revision" =~ ^[0-9A-Za-z._+-]+$ ]]; then
  echo 'Invalid revision.' >&2
  exit 2
fi
repository="${SC_RELEASE_REPOSITORY:-${GITHUB_REPOSITORY:-$(go list -m)}}"
repository="${repository#github.com/}"
repository="${repository,,}"
if [[ ! "$repository" =~ ^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$ ]]; then
  echo 'SC_RELEASE_REPOSITORY must be an owner/repository name.' >&2
  exit 2
fi

# Build inputs are explicit: never silently ship an API-only control plane.
for file in backend/internal/webui/dist/index.html backend/internal/webui/dist/THIRD_PARTY_LICENSES.txt bin/scrcpy-server/scrcpy-server; do
  test -s "$file" || { echo "Missing $file; run the corresponding make build target first." >&2; exit 1; }
done
for abi in arm64-v8a armeabi-v7a x86_64 x86; do
  test -s "bin/agent/$abi/scrcpycat-agent" || { echo "Missing Android Agent for $abi; run make build-agent-docker." >&2; exit 1; }
done

output="$root/dist/release/$version"
mkdir -p "$output"
staging="$(mktemp -d "${TMPDIR:-/tmp}/scrcpycat-release.XXXXXX")"
trap 'rm -rf -- "$staging"' EXIT
common="$staging/common"
mkdir -p "$common/bin" "$common/frontend" "$common/third_party/scrcpy" "$common/third_party/gadb" "$common/deploy"
cp "LICENSE" "NOTICE" "readme.md" "$common/"
cp "frontend/LICENSE" "$common/frontend/"
cp "third_party/scrcpy/LICENSE" "$common/third_party/scrcpy/"
cp "third_party/gadb/LICENSE" "$common/third_party/gadb/"
cp -R "third_party/licenses" "$common/third_party/"
cp -R "bin/agent" "bin/scrcpy-server" "$common/bin/"
cp "deploy/docker-compose.yml" "deploy/README.md" "$common/deploy/"
sed -e "s|^SCRCPYCAT_IMAGE=.*|SCRCPYCAT_IMAGE=ghcr.io/$repository:$version|" \
    -e "s|^SCRCPYCAT_ADB_IMAGE=.*|SCRCPYCAT_ADB_IMAGE=ghcr.io/$repository-adb-deployer:$version|" \
    "deploy/.env.example" > "$common/deploy/.env.example"
cp "backend/internal/webui/dist/THIRD_PARTY_LICENSES.txt" "$common/THIRD_PARTY_FRONTEND_LICENSES.txt"
CGO_ENABLED=0 go run ./tools/licenses -out "$common/THIRD_PARTY_GO_LICENSES.txt"
printf 'Version: %s\nRevision: %s\n' "$version" "$revision" > "$common/BUILDINFO"
chmod -R a+rX "$common"

archives=()
ldflags="-s -w -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Version=$version -X github.com/cwithw/ScrcpyCat/internal/buildinfo.Revision=$revision"
for arch in amd64 arm64; do
  name="scrcpycat-$version-linux-$arch"
  bundle="$staging/$name"
  cp -R "$common" "$bundle"
  for component in controlplane agent adb-deployer; do
    case "$component" in
      controlplane) package='./backend/cmd/controlplane' ;;
      agent) package='./agent/cmd/agent' ;;
      adb-deployer) package='./adb-deployer/cmd/adb-deployer' ;;
    esac
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="$ldflags" -o "$bundle/bin/scrcpycat-$component" "$package"
  done
  tar -C "$staging" -czf "$output/$name.tar.gz" "$name"
  archives+=("$name.tar.gz")
done

# Also offer the common Android artifacts without the Linux executables.
name="scrcpycat-$version-android"
mv "$common" "$staging/$name"
tar -C "$staging" -czf "$output/$name.tar.gz" "$name"
archives+=("$name.tar.gz")
(cd "$output" && sha256sum "${archives[@]}" > "SHA256SUMS")
printf 'Release files: %s\n' "$output"
