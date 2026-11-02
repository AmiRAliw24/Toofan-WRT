#!/bin/sh
# Builds the web UI, cross-compiles the panel and packs a release tarball.
set -e
cd "$(dirname "$0")"
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}"
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
# Two Xray engines for armv7, both official XTLS releases checked against
# their published SHA-256:
# - main: the newest release;
# - compat: 26.3.27, the newest that still allows VLESS/Trojan without TLS
#   (later releases refuse it).
XRAY_VERSION=26.10.31
XRAY_ZIP_SHA256=2387f55239ee7799fd67d5382e79f589c1da07a97d993e86b058f2290b8e456b
XRAY_COMPAT_VERSION=26.3.27
XRAY_COMPAT_ZIP_SHA256=c7265ae13c63ca0241a037df4ef960ad37938c8a67d984cc08834b2cfdf5654b
xray_fetch() { # version sha256 output
	[ -f "$3" ] && return 0
	curl -fsSL -o openwrt/pkgs/xray/arm.zip "https://github.com/XTLS/Xray-core/releases/download/v$1/Xray-linux-arm32-v7a.zip"
	echo "$2  openwrt/pkgs/xray/arm.zip" | sha256sum -c -
	python3 -I -c 'import sys, zipfile; open(sys.argv[2], "wb").write(zipfile.ZipFile(sys.argv[1]).read("xray"))' \
		openwrt/pkgs/xray/arm.zip "$3"
	rm -f openwrt/pkgs/xray/arm.zip
}
mkdir -p openwrt/pkgs/xray
xray_fetch "$XRAY_VERSION" "$XRAY_ZIP_SHA256" "openwrt/pkgs/xray/xray-$XRAY_VERSION-armv7"
xray_fetch "$XRAY_COMPAT_VERSION" "$XRAY_COMPAT_ZIP_SHA256" "openwrt/pkgs/xray/xray-compat-$XRAY_COMPAT_VERSION-armv7"
cp "openwrt/pkgs/xray/xray-$XRAY_VERSION-armv7" "$OUT/bin/xray-armv7"
cp "openwrt/pkgs/xray/xray-compat-$XRAY_COMPAT_VERSION-armv7" "$OUT/bin/xray-compat-armv7"
chmod 755 "$OUT/bin/xray-armv7" "$OUT/bin/xray-compat-armv7"

for list in openwrt/offline-*.list; do bundle "$list" .; done
for list in openwrt/optional-*.list; do bundle "$list" optional; done

tar -C dist -czf "dist/toofanwrt-$VERSION.tar.gz" toofanwrt
ls -la dist
