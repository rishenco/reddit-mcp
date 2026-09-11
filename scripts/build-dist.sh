#!/usr/bin/env bash
#
# Cross-compiles reddit-mcp into release archives under dist/, one per platform,
# plus a checksums.txt that `sha256sum -c` understands.
#
# Usage: scripts/build-dist.sh [version]
#
# The version is taken from the argument, then $VERSION, then `git describe`,
# and is stamped into the binary so `reddit-mcp --version` reports it.

set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:-${VERSION:-}}"
if [[ -z $version ]]; then
	version="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

dist="${DIST:-dist}"
platforms=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)

# Archive names carry a bare version, the way release assets usually read:
# tag v0.1.0 -> reddit-mcp_0.1.0_linux_amd64.tar.gz.
file_version="${version#v}"

rm -rf "$dist"
mkdir -p "$dist"
dist_abs="$(cd "$dist" && pwd)"

stage="$dist/.stage"

for platform in "${platforms[@]}"; do
	goos="${platform%%/*}"
	goarch="${platform##*/}"

	binary="reddit-mcp"
	if [[ $goos == windows ]]; then
		binary="reddit-mcp.exe"
	fi

	archive="reddit-mcp_${file_version}_${goos}_${goarch}"

	echo "building $archive"

	rm -rf "$stage"
	mkdir -p "$stage"

	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
		-trimpath \
		-ldflags "-s -w -X main.version=$version" \
		-o "$stage/$binary" \
		./cmd/reddit-mcp

	cp README.md "$stage/README.md"

	# Archive contents sit at the root, so unpacking drops the binary straight
	# into the current directory.
	if [[ $goos == windows ]]; then
		(cd "$stage" && zip -q "$dist_abs/$archive.zip" "$binary" README.md)
	else
		tar -czf "$dist_abs/$archive.tar.gz" -C "$stage" "$binary" README.md
	fi
done

rm -rf "$stage"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	else
		shasum -a 256 "$@"
	fi
}

# The glob keeps checksums.txt from hashing itself.
(cd "$dist_abs" && sha256 reddit-mcp_* >checksums.txt)

echo
echo "dist/:"
ls -1 "$dist_abs"
