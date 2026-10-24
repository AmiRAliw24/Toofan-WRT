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
# Optional features: OpenVPN, UPnP, SQM and per-Wi-Fi routing. Failures here are not fatal.
OPTIONAL="openvpn-mbedtls miniupnpd-nftables tc-tiny kmod-sched-cake kmod-ifb kmod-nft-bridge"
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
# Prefer the bundled, newer Xray when it runs on this router; the package's
# /usr/bin/xray stays as the fallback.
XRAY=/usr/bin/xray
case "$ARCH" in armv7l) XBIN=bin/xray-armv7 ;; *) XBIN="" ;; esac
if [ -n "$XBIN" ] && [ -f "$XBIN" ]; then
	mkdir -p /usr/libexec/separ
	cp "$XBIN" /usr/libexec/separ/xray.new && chmod 755 /usr/libexec/separ/xray.new
	if /usr/libexec/separ/xray.new version >/dev/null 2>&1; then
		mv -f /usr/libexec/separ/xray.new /usr/libexec/separ/xray
		XRAY=/usr/libexec/separ/xray
	else
		rm -f /usr/libexec/separ/xray.new
		say "bundled Xray does not run here; using the system package"
	fi
fi
say "xray: $($XRAY version 2>/dev/null | head -1)"

say "installing panel"
# From here on the panel is down; whatever happens next, bring it back up.
trap '[ -x /etc/init.d/separ ] && { /etc/init.d/separ enable; /etc/init.d/separ start; } >/dev/null 2>&1' EXIT
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
# for old bookmarks). LuCI is removed below once the panel answers.
uci set separ.main.listen=':80,:8080'
uci set separ.main.xray="$XRAY"
uci commit separ
if [ -x /etc/init.d/uhttpd ]; then
	say "making Separ the main interface (LuCI moved out of the way)"
	uci -q delete uhttpd.main.listen_http || true
	uci add_list uhttpd.main.listen_http='0.0.0.0:8081'
	uci -q delete uhttpd.main.listen_https || true
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

# One interface only: remove LuCI, but only after Separ is confirmed to be
# answering, so the router is never left without a web interface.
# KEEP_LUCI=1 sh install.sh keeps it. It can be put back from the panel.
if [ "$KEEP_LUCI" != 1 ] && [ "$PM" = opkg ] && opkg list-installed | grep -qE '^(luci|uhttpd)'; then
	P0="$(uci -q get separ.main.listen | cut -d, -f1 | sed 's/.*://')"
	i=0; up=0
	while [ $i -lt 20 ]; do
		if wget -q -T 2 -O - "http://127.0.0.1:${P0:-80}/api/ping" 2>/dev/null | grep -q version; then up=1; break; fi
		sleep 1; i=$((i+1))
	done
	if [ $up = 1 ]; then
		say "removing LuCI (Separ is now the only interface)"
		/etc/init.d/uhttpd stop 2>/dev/null || true
		/etc/init.d/uhttpd disable 2>/dev/null || true
		LP="$(opkg list-installed | cut -d' ' -f1 | grep -E '^(luci|uhttpd|liblucihttp)' | tr '\n' ' ')"
		# shellcheck disable=SC2086
		opkg remove $LP >/dev/null 2>&1 || opkg remove --force-depends $LP >/dev/null 2>&1 || say "LuCI could not be fully removed"
		rm -rf /www/luci-static /www/cgi-bin/luci /tmp/luci-* 2>/dev/null || true
		[ -f /www/index.html ] && grep -q luci /www/index.html && rm -f /www/index.html || true
	else
		say "panel did not answer yet; LuCI was kept (it stays off)"
	fi
fi

# /tmp lives in RAM: drop the release files once installed (the panel's own
# updater cleans up after itself).
if [ -z "$SEPAR_UPDATE" ]; then
	HERE="$(pwd)"
	case "$HERE" in /tmp/*) cd / && rm -rf "$HERE" ;; esac
	rm -f /tmp/separ-*.tar.gz /tmp/separ-update/*.tar.gz 2>/dev/null || true
	rm -rf /tmp/separ-update/separ 2>/dev/null || true
fi

LANIP="$(uci -q get network.lan.ipaddr | cut -d/ -f1)"
PORT="$(uci -q get separ.main.listen | cut -d, -f1 | sed 's/.*://')"
say "done!"
echo
if [ "${PORT:-80}" = 80 ]; then
	echo "  پنل:  http://${LANIP:-192.168.1.1}"
else
	echo "  پنل:  http://${LANIP:-192.168.1.1}:$PORT"
fi
echo "  رمز:  همان رمز root روتر (SSH)"
echo
