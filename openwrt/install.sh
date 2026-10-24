#!/bin/sh
# Separ installer. Run on the router from the extracted release folder:
#   cd /tmp/separ && sh install.sh
set -e
cd "$(dirname "$0")"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mخطا / error:\033[0m %s\n' "$*" >&2; exit 1; }

[ -f /etc/openwrt_release ] || die "this is not OpenWrt"
. /etc/openwrt_release
ARCH="$(uname -m)"
case "$ARCH" in
	armv7l) BIN=separ-armv7 ;;
	aarch64) BIN=separ-arm64 ;;
	x86_64) BIN=separ-amd64 ;;
	mips*) BIN=separ-mipsle ;;
	*) die "unsupported CPU: $ARCH" ;;
esac
[ -f "bin/$BIN" ] || die "bin/$BIN missing from the release"

if command -v apk >/dev/null 2>&1; then
	PM=apk
	pm_update() { apk update; }
	pm_install() { apk add "$@"; }
	pm_has() { apk info -e "$1" >/dev/null 2>&1; }
else
	PM=opkg
	pm_update() { opkg update; }
	pm_install() { opkg install "$@"; }
	pm_has() { opkg list-installed | grep -q "^$1 "; }
fi

say "OpenWrt $DISTRIB_RELEASE ($DISTRIB_TARGET), package manager: $PM"

DEPS="xray-core v2ray-geoip v2ray-geosite-ir kmod-nft-tproxy ca-bundle"
MISSING=""
for p in $DEPS; do pm_has "$p" || MISSING="$MISSING $p"; done
if [ -n "$MISSING" ]; then
	say "installing:$MISSING"
	pm_update || die "package list update failed (is the router online?)"
	# shellcheck disable=SC2086
	pm_install $MISSING || die "could not install:$MISSING"
fi
command -v xray >/dev/null || die "xray binary not found after install"
say "xray: $(xray version 2>/dev/null | head -1)"

say "installing panel"
/etc/init.d/separ stop 2>/dev/null || true
cp "bin/$BIN" /usr/bin/separ
chmod 755 /usr/bin/separ
cp files/etc/init.d/separ /etc/init.d/separ
chmod 755 /etc/init.d/separ
mkdir -p /lib/upgrade/keep.d /etc/hotplug.d/iface /etc/separ
cp files/lib/upgrade/keep.d/separ /lib/upgrade/keep.d/separ
cp files/etc/hotplug.d/iface/99-separ /etc/hotplug.d/iface/99-separ
cp uninstall.sh /usr/share/separ-uninstall.sh 2>/dev/null || true

[ -f /etc/config/separ ] || {
	touch /etc/config/separ
	uci set separ.main=separ
	uci set separ.main.listen=':8080'
	uci commit separ
}

# Software flow offloading skips nftables for established connections,
# which would hide per-device speeds and break per-device limits.
if [ "$(uci -q get firewall.@defaults[0].flow_offloading)" = "1" ]; then
	say "disabling software flow offloading (needed for per-device stats/limits)"
	uci set firewall.@defaults[0].flow_offloading=0
	uci commit firewall
	/etc/init.d/firewall reload
fi

# Xray's own init script must not start a second copy.
[ -x /etc/init.d/xray ] && { /etc/init.d/xray stop 2>/dev/null; /etc/init.d/xray disable 2>/dev/null; } || true

/etc/init.d/separ enable
/etc/init.d/separ start

LANIP="$(uci -q get network.lan.ipaddr | cut -d/ -f1)"
PORT="$(uci -q get separ.main.listen | sed 's/.*://')"
say "done!"
echo
echo "  پنل:  http://${LANIP:-192.168.1.1}:${PORT:-8080}"
echo "  رمز:  همان رمز root روتر (LuCI/SSH)"
echo
