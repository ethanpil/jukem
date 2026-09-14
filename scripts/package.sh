#!/bin/sh
# Build the jukem binary and an .apk for one architecture.
# Usage:   scripts/package.sh <amd64|arm64> <version>
# Signing: set APK_SIGNING_KEY to the private key path; unset gives an unsigned dev build.
set -eu

arch="$1"
version="${2#v}"

case "$arch" in
	amd64) apkarch=x86_64 ;;
	arm64) apkarch=aarch64 ;;
	*) echo "unsupported arch: $arch (jukem builds amd64 and arm64)" >&2; exit 1 ;;
esac

# apk writes a pre-release suffix with an underscore: v1.3.0-rc1 becomes 1.3.0_rc1.
apkver=$(printf '%s' "$version" | sed 's/-/_/')

mkdir -p build dist
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
	go build -trimpath -ldflags "-s -w -X main.version=$version" \
	-o build/jukem ./cmd/jukem

out="dist/jukem-$apkver-$apkarch.apk"
NFPM_ARCH="$arch" VERSION="$apkver" \
	go tool nfpm package --packager apk --config packaging/nfpm.yaml --target "$out"

if [ -n "${APK_SIGNING_KEY:-}" ]; then
	go run ./tools/apksign -key "$APK_SIGNING_KEY" -name jukem.rsa.pub "$out"
fi
