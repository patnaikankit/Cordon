#!/usr/bin/env bash
set -euo pipefail

# Reproducible, hermetic build script for Cordon binaries
VERSION="${VERSION:-0.1.0}"
GIT_COMMIT="${GIT_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUTPUT_DIR="${OUTPUT_DIR:-bin}"

mkdir -p "${OUTPUT_DIR}"

echo "Building Cordon binaries..."
echo "  Version:    ${VERSION}"
echo "  Commit:     ${GIT_COMMIT}"
echo "  Build Date: ${BUILD_DATE}"

# CGO_ENABLED=0 ensures pure Go static binaries with zero dynamic C library dependencies.
# -trimpath removes host filesystem paths from debug symbols for bit-for-bit reproducible builds.
CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.date=${BUILD_DATE}" \
    -o "${OUTPUT_DIR}/cordon-server" \
    ./cmd/cordon-server

# Compute SHA256 checksum
if command -v shasum >/dev/null 2>&1; then
    (cd "${OUTPUT_DIR}" && shasum -a 256 cordon-server > cordon-server.sha256)
elif command -v sha256sum >/dev/null 2>&1; then
    (cd "${OUTPUT_DIR}" && sha256sum cordon-server > cordon-server.sha256)
fi

echo "Build complete: ${OUTPUT_DIR}/cordon-server"
if [ -f "${OUTPUT_DIR}/cordon-server.sha256" ]; then
    echo "Checksum: $(cat "${OUTPUT_DIR}/cordon-server.sha256")"
fi
