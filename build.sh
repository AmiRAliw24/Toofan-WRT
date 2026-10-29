#!/bin/sh
# Builds the web UI, cross-compiles the panel and packs a release tarball.
set -e
cd "$(dirname "$0")"
VERSION="${VERSION:-$(date +%Y.%m.%d)-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
OUT=dist/toofanwrt
rm -rf dist && mkdir -p "$OUT/bin"

(cd frontend && npm ci --silent && npx tsc && npx vite build)

LDFLAGS="-s -w -X main.version=$VERSION"
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$LDFLAGS" -o "../$OUT/bin/toofanwrt-armv7" .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "../$OUT/bin/toofanwrt-arm64" .
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
# Two Xray engines for armv7:
# - main: the newest official release (checked against its published SHA-256);
# - compat: 26.3.27, the newest that still allows VLESS/Trojan without TLS
#   (later releases refuse it), from OpenWrt 25.12's feed (apk-v3 container).
XRAY_VERSION=26.10.29
XRAY_ZIP_URL="https://github.com/XTLS/Xray-core/releases/download/v$XRAY_VERSION/Xray-linux-arm32-v7a.zip"
XRAY_ZIP_SHA256=263fa7325c63b3a7f7316405f561e7c790b44c1461992ea648007f0374e9bdf4
XRAY_COMPAT_APK_URL="https://downloads.openwrt.org/releases/25.12.6/packages/arm_cortex-a7_neon-vfpv4/packages/xray-core-26.3.27-r1.apk"
mkdir -p openwrt/pkgs/xray
[ -f "openwrt/pkgs/xray/xray-$XRAY_VERSION-armv7" ] || {
	curl -fsSL -o openwrt/pkgs/xray/arm.zip "$XRAY_ZIP_URL"
	echo "$XRAY_ZIP_SHA256  openwrt/pkgs/xray/arm.zip" | sha256sum -c -
	python3 -I -c 'import sys, zipfile; open(sys.argv[2], "wb").write(zipfile.ZipFile(sys.argv[1]).read("xray"))' \
		openwrt/pkgs/xray/arm.zip "openwrt/pkgs/xray/xray-$XRAY_VERSION-armv7"
	rm -f openwrt/pkgs/xray/arm.zip
}
[ -f openwrt/pkgs/xray/xray-compat-armv7 ] || {
	curl -fsSL -o openwrt/pkgs/xray/arm.apk "$XRAY_COMPAT_APK_URL"
	python3 tools/apk-extract-elf.py openwrt/pkgs/xray/arm.apk openwrt/pkgs/xray/xray-compat-armv7
	rm -f openwrt/pkgs/xray/arm.apk
}
cp "openwrt/pkgs/xray/xray-$XRAY_VERSION-armv7" "$OUT/bin/xray-armv7"
cp openwrt/pkgs/xray/xray-compat-armv7 "$OUT/bin/xray-compat-armv7"
chmod 755 "$OUT/bin/xray-armv7" "$OUT/bin/xray-compat-armv7"

for list in openwrt/offline-*.list; do bundle "$list" .; done
for list in openwrt/optional-*.list; do bundle "$list" optional; done

tar -C dist -czf "dist/toofanwrt-$VERSION.tar.gz" toofanwrt
ls -la dist
