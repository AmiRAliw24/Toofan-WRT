#!/bin/sh
# Builds the web UI, cross-compiles the panel and packs a release tarball.
set -e
cd "$(dirname "$0")"
VERSION="${VERSION:-$(date +%Y.%m.%d)-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
OUT=dist/separ
rm -rf dist && mkdir -p "$OUT/bin"

(cd frontend && npm ci --silent && npx tsc && npx vite build)

LDFLAGS="-s -w -X main.version=$VERSION"
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$LDFLAGS" -o "../$OUT/bin/separ-armv7" .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "../$OUT/bin/separ-arm64" .
cd ..

cp -r openwrt/files openwrt/install.sh openwrt/uninstall.sh "$OUT/"

# Offline package bundles, for routers that can't reach downloads.openwrt.org.
# offline-*.list are required, optional-*.list enable extra features.
bundle() { # list-file dest-subdir
	rel="$(basename "$1" .list)"; rel="${rel#*-}"
	mkdir -p "$OUT/pkgs/$rel/$2"
	grep -v '^#' "$1" | while read -r path; do
		[ -n "$path" ] || continue
		f="openwrt/pkgs/$rel/$(basename "$path")"
		[ -f "$f" ] || { mkdir -p "openwrt/pkgs/$rel"; curl -fsSL -o "$f" "https://downloads.openwrt.org/releases/$rel/$path"; }
		cp "$f" "$OUT/pkgs/$rel/$2"
	done
}
# Newest Xray that still allows VLESS without TLS (26.10+ forbids it), taken
# from OpenWrt 25.12's package feed and unpacked from its apk-v3 container.
XRAY_APK_URL="https://downloads.openwrt.org/releases/25.12.6/packages/arm_cortex-a7_neon-vfpv4/packages/xray-core-26.3.27-r1.apk"
mkdir -p openwrt/pkgs/xray
[ -f openwrt/pkgs/xray/xray-armv7 ] || {
	curl -fsSL -o openwrt/pkgs/xray/arm.apk "$XRAY_APK_URL"
	python3 tools/apk-extract-elf.py openwrt/pkgs/xray/arm.apk openwrt/pkgs/xray/xray-armv7
	rm -f openwrt/pkgs/xray/arm.apk
}
cp openwrt/pkgs/xray/xray-armv7 "$OUT/bin/xray-armv7"

for list in openwrt/offline-*.list; do bundle "$list" .; done
for list in openwrt/optional-*.list; do bundle "$list" optional; done

tar -C dist -czf "dist/separ-$VERSION.tar.gz" separ
ls -la dist
