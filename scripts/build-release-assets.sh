#!/usr/bin/env bash
set -euo pipefail

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  echo "usage: $0 <tag>" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
TMP_DIR="$(mktemp -d)"
GO_CACHE_DIR="$(mktemp -d)"
GO_MOD_CACHE_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "$TMP_DIR"
  rm -rf "$GO_CACHE_DIR"
  rm -rf "$GO_MOD_CACHE_DIR"
}
trap cleanup EXIT

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

binaries=(
  "mcp-smoke-agent ./cmd/mcp-smoke-agent"
)

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  else
    shasum -a 256 "$1"
  fi
}

for binary_entry in "${binaries[@]}"; do
  read -r binary_name binary_path <<<"$binary_entry"

  for target in "${targets[@]}"; do
    read -r goos goarch <<<"$target"

    asset_base="${binary_name}_${TAG}_${goos}_${goarch}"
    output_name="${binary_name}"
    if [[ "$goos" == "windows" ]]; then
      output_name="${output_name}.exe"
    fi

    build_dir="${TMP_DIR}/${binary_name}-${goos}-${goarch}"
    mkdir -p "$build_dir"

    echo "building ${binary_name} for ${goos}/${goarch}"
    (
      cd "$ROOT_DIR"
      GOCACHE="$GO_CACHE_DIR" GOMODCACHE="$GO_MOD_CACHE_DIR" \
      CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags="-s -w" -o "${build_dir}/${output_name}" "$binary_path"
    )

    cp "${ROOT_DIR}/LICENSE" "$build_dir/"
    cp "${ROOT_DIR}/README.md" "$build_dir/"

    if [[ "$goos" == "windows" ]]; then
      (
        cd "$build_dir"
        zip -q "${DIST_DIR}/${asset_base}.zip" "$output_name" LICENSE README.md
      )
    else
      tar -C "$build_dir" -czf "${DIST_DIR}/${asset_base}.tar.gz" "$output_name" LICENSE README.md
    fi
  done
done

(
  cd "$DIST_DIR"
  : > checksums.txt
  for asset in *; do
    if [[ "$asset" == "checksums.txt" ]]; then
      continue
    fi
    sha256_file "$asset" >> checksums.txt
  done
)

echo "release assets written to ${DIST_DIR}"
