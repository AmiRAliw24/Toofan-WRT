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
# Bundled packages first: they work even when downloads.openwrt.org is unreachable.
if [ -n "$MISSING" ] && [ "$PM" = opkg ] && [ -d "pkgs/$DISTRIB_RELEASE" ]; then
	say "installing bundled packages for $DISTRIB_RELEASE"
	for p in $MISSING; do
		f="$(ls "pkgs/$DISTRIB_RELEASE/${p}_"*.ipk 2>/dev/null | head -1)"
		[ -n "$f" ] && LOCAL="$LOCAL $f"
	done
	# kmods first so xray's deps are all satisfied in one pass
	# shellcheck disable=SC2086
	[ -n "$LOCAL" ] && opkg install $(ls pkgs/$DISTRIB_RELEASE/kmod-nf-tproxy_*.ipk 2>/dev/null) $LOCAL || true
	MISSING=""
	for p in $DEPS; do pm_has "$p" || MISSING="$MISSING $p"; done
fi

if [ -n "$MISSING" ]; then
	say "installing:$MISSING"
	if ! pm_update; then
		echo
		echo "  روتر به downloads.openwrt.org دسترسی ندارد. این‌ها را چک کنید:"
		echo "    ping -c 3 8.8.8.8                 # اینترنت روتر"
		echo "    nslookup downloads.openwrt.org    # DNS"
		echo
		die "package list update failed"
	fi
	# shellcheck disable=SC2086
	pm_install $MISSING || die "could not install:$MISSING"
fi
# Optional features: OpenVPN, UPnP and SQM. Failures here are not fatal.
OPTIONAL="openvpn-mbedtls miniupnpd-nftables tc-tiny kmod-sched-cake kmod-ifb"
OPT_MISSING=""
for p in $OPTIONAL; do pm_has "$p" || OPT_MISSING="$OPT_MISSING $p"; done
if [ -n "$OPT_MISSING" ]; then
	if [ "$PM" = opkg ] && [ -d "pkgs/$DISTRIB_RELEASE/optional" ]; then
		say "installing optional packages (OpenVPN, UPnP, SQM)"
		# shellcheck disable=SC2046
		opkg install $(ls pkgs/$DISTRIB_RELEASE/optional/*.ipk) >/dev/null 2>&1 || true
	else
		say "installing optional packages:$OPT_MISSING"
		# shellcheck disable=SC2086
		pm_install $OPT_MISSING >/dev/null 2>&1 || say "some optional packages could not be installed (features stay off)"
	fi
fi
# miniupnpd stays off until switched on from the panel.
[ -x /etc/init.d/miniupnpd ] && [ "$(uci -q get upnpd.config.enabled)" != "1" ] && /etc/init.d/miniupnpd disable 2>/dev/null || true

command -v xray >/dev/null || die "xray binary not found after install"
say "xray: $(xray version 2>/dev/null | head -1)"

say "installing panel"
/etc/init.d/separ stop 2>/dev/null || true
# Wait for the old process to exit, then swap the binary atomically: writing
# over a running executable fails with "Text file busy".
i=0
while pidof separ >/dev/null 2>&1 && [ $i -lt 15 ]; do sleep 1; i=$((i+1)); done
pidof separ >/dev/null 2>&1 && kill -9 $(pidof separ) 2>/dev/null || true
cp "bin/$BIN" /usr/bin/separ.new
chmod 755 /usr/bin/separ.new
mv -f /usr/bin/separ.new /usr/bin/separ
cp files/etc/init.d/separ /etc/init.d/separ
chmod 755 /etc/init.d/separ
mkdir -p /lib/upgrade/keep.d /etc/hotplug.d/iface /etc/separ
cp files/lib/upgrade/keep.d/separ /lib/upgrade/keep.d/separ
cp files/etc/hotplug.d/iface/99-separ /etc/hotplug.d/iface/99-separ
cp uninstall.sh /usr/share/separ-uninstall.sh 2>/dev/null || true

[ -f /etc/config/separ ] || touch /etc/config/separ
uci -q get separ.main >/dev/null || uci set separ.main=separ
# Separ is the router's single web interface on port 80 (8080 stays as an alias
# for old bookmarks). LuCI stays installed but off; the panel can start it
# temporarily on port 8081 (System -> Advanced settings).
uci set separ.main.listen=':80,:8080'
uci commit separ
if [ -x /etc/init.d/uhttpd ]; then
	say "making Separ the main interface (LuCI moved out of the way)"
	uci -q delete uhttpd.main.listen_http
	uci add_list uhttpd.main.listen_http='0.0.0.0:8081'
	uci -q delete uhttpd.main.listen_https
	uci set uhttpd.main.redirect_https='0'
	uci commit uhttpd
	/etc/init.d/uhttpd stop 2>/dev/null || true
	/etc/init.d/uhttpd disable 2>/dev/null || true
fi

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
PORT="$(uci -q get separ.main.listen | cut -d, -f1 | sed 's/.*://')"
say "done!"
echo
if [ "${PORT:-80}" = 80 ]; then
	echo "  پنل:  http://${LANIP:-192.168.1.1}"
else
	echo "  پنل:  http://${LANIP:-192.168.1.1}:$PORT"
fi
echo "  رمز:  همان رمز root روتر (LuCI/SSH)"
echo
