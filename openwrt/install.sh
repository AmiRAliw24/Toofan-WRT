#!/bin/sh
# ToofanWrt installer. Run on the router from the extracted release folder:
#   cd /tmp/toofanwrt && sh install.sh
set -e
cd "$(dirname "$0")"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mخطا / error:\033[0m %s\n' "$*" >&2; exit 1; }

[ -f /etc/openwrt_release ] || die "this is not OpenWrt"
. /etc/openwrt_release
ARCH="$(uname -m)"
case "$ARCH" in
	armv7l) BIN=toofanwrt-armv7 ;;
	aarch64) BIN=toofanwrt-arm64 ;;
	x86_64) BIN=toofanwrt-amd64 ;;
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
# Prefer the bundled Xray when it runs on this router; the package's
# /usr/bin/xray stays as the fallback. Two bundled engines: the newest one,
# and 26.3.27 for servers newer releases refuse (VLESS/Trojan without TLS).
install_xray() { # <bundled file> <name>: prints the installed path on success
	[ -f "$1" ] || return 1
	mkdir -p /usr/libexec/toofanwrt
	cp "$1" "/usr/libexec/toofanwrt/$2.new" && chmod 755 "/usr/libexec/toofanwrt/$2.new"
	if "/usr/libexec/toofanwrt/$2.new" version >/dev/null 2>&1; then
		mv -f "/usr/libexec/toofanwrt/$2.new" "/usr/libexec/toofanwrt/$2"
		echo "/usr/libexec/toofanwrt/$2"
		return 0
	fi
	rm -f "/usr/libexec/toofanwrt/$2.new"
	return 1
}
XRAY=/usr/bin/xray
XRAY_COMPAT=""
case "$ARCH" in
	armv7l)
		XRAY="$(install_xray bin/xray-armv7 xray)" || { XRAY=/usr/bin/xray; say "bundled Xray does not run here; using the system package"; }
		XRAY_COMPAT="$(install_xray bin/xray-compat-armv7 xray-compat)" || XRAY_COMPAT=""
		;;
esac
say "xray: $($XRAY version 2>/dev/null | head -1)"
[ -n "$XRAY_COMPAT" ] && say "xray (compat, for VLESS/Trojan without TLS): $($XRAY_COMPAT version 2>/dev/null | head -1)"

# The panel used to be called Separ. Its settings and data are copied over
# and the old service is stopped; the old files are only deleted once the
# new panel answers (the old updater may run this from /tmp/separ-update and
# needs /usr/bin/separ.prev to roll back), so nothing is lost if this fails.
MIGRATED=0
if [ -e /etc/init.d/separ ] || [ -d /etc/separ ] || [ -f /etc/config/separ ]; then
	say "migrating Separ -> ToofanWrt (settings are kept)"
	MIGRATED=1
	/etc/init.d/separ stop 2>/dev/null || true
	/etc/init.d/separ disable 2>/dev/null || true
	i=0
	while pidof separ >/dev/null 2>&1 && [ $i -lt 15 ]; do sleep 1; i=$((i+1)); done
	pidof separ >/dev/null 2>&1 && kill -9 $(pidof separ) 2>/dev/null || true
	if [ -d /etc/separ ] && [ ! -d /etc/toofanwrt ]; then cp -a /etc/separ /etc/toofanwrt; fi
	if [ -f /etc/config/separ ] && [ ! -s /etc/config/toofanwrt ]; then
		sed 's/^config separ /config toofanwrt /' /etc/config/separ > /etc/config/toofanwrt
	fi
	nft delete table inet separ 2>/dev/null || true
	nft delete table bridge separ 2>/dev/null || true
	for d in /tmp/dnsmasq.d /tmp/dnsmasq.*.d; do rm -f "$d/separ.conf" 2>/dev/null || true; done
fi

say "installing panel"
# From here on the panel is down; whatever happens next, bring it back up.
trap '[ -x /etc/init.d/toofanwrt ] && { /etc/init.d/toofanwrt enable; /etc/init.d/toofanwrt start; } >/dev/null 2>&1' EXIT
/etc/init.d/toofanwrt stop 2>/dev/null || true
# Wait for the old process to exit, then swap the binary atomically: writing
# over a running executable fails with "Text file busy".
i=0
while pidof toofanwrt >/dev/null 2>&1 && [ $i -lt 15 ]; do sleep 1; i=$((i+1)); done
pidof toofanwrt >/dev/null 2>&1 && kill -9 $(pidof toofanwrt) 2>/dev/null || true
cp "bin/$BIN" /usr/bin/toofanwrt.new
chmod 755 /usr/bin/toofanwrt.new
mv -f /usr/bin/toofanwrt.new /usr/bin/toofanwrt
cp files/etc/init.d/toofanwrt /etc/init.d/toofanwrt
chmod 755 /etc/init.d/toofanwrt
mkdir -p /lib/upgrade/keep.d /etc/hotplug.d/iface /etc/toofanwrt
cp files/lib/upgrade/keep.d/toofanwrt /lib/upgrade/keep.d/toofanwrt
cp files/etc/hotplug.d/iface/99-toofanwrt /etc/hotplug.d/iface/99-toofanwrt
cp uninstall.sh /usr/share/toofanwrt-uninstall.sh 2>/dev/null || true

[ -f /etc/config/toofanwrt ] || touch /etc/config/toofanwrt
uci -q get toofanwrt.main >/dev/null || uci set toofanwrt.main=toofanwrt
# ToofanWrt is the router's single web interface on port 80 (8080 stays as an alias
# for old bookmarks). LuCI is removed below once the panel answers.
uci set toofanwrt.main.listen=':80,:8080'
uci set toofanwrt.main.xray="$XRAY"
uci set toofanwrt.main.xray_compat="$XRAY_COMPAT"
uci commit toofanwrt
if [ -x /etc/init.d/uhttpd ]; then
	say "making ToofanWrt the main interface (LuCI moved out of the way)"
	uci -q delete uhttpd.main.listen_http || true
	uci add_list uhttpd.main.listen_http='0.0.0.0:8081'
	uci -q delete uhttpd.main.listen_https || true
	uci set uhttpd.main.redirect_https='0'
	uci commit uhttpd
	/etc/init.d/uhttpd stop 2>/dev/null || true
	/etc/init.d/uhttpd disable 2>/dev/null || true
fi

# Router identity: name it ToofanWrt unless the owner already chose a name,
# and greet SSH logins with it (OpenWrt's banner is kept as banner.openwrt).
if [ "$(uci -q get system.@system[0].hostname)" = "OpenWrt" ]; then
	uci set system.@system[0].hostname='ToofanWrt'
	uci commit system
	echo ToofanWrt > /proc/sys/kernel/hostname 2>/dev/null || true
fi
[ -f /etc/banner ] && [ ! -f /etc/banner.openwrt ] && cp /etc/banner /etc/banner.openwrt
LANIP0="$(uci -q get network.lan.ipaddr | cut -d/ -f1)"
sed "s#http://192.168.1.1#http://${LANIP0:-192.168.1.1}#" files/etc/banner > /etc/banner 2>/dev/null || true

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

/etc/init.d/toofanwrt enable
/etc/init.d/toofanwrt start

# panel_up waits up to 30 s for the new panel to answer.
PANEL_UP=0
panel_up() {
	[ $PANEL_UP = 1 ] && return 0
	P0="$(uci -q get toofanwrt.main.listen | cut -d, -f1 | sed 's/.*://')"
	i=0
	while [ $i -lt 30 ]; do
		if wget -q -T 2 -O - "http://127.0.0.1:${P0:-80}/api/ping" 2>/dev/null | grep -q version; then PANEL_UP=1; return 0; fi
		sleep 1; i=$((i+1))
	done
	return 1
}

if [ $MIGRATED = 1 ]; then
	if panel_up; then
		say "removing the old Separ files"
		rm -rf /etc/separ /usr/libexec/separ /tmp/separ-restarting
		rm -f /etc/config/separ /etc/init.d/separ /usr/bin/separ /usr/bin/separ.prev /usr/bin/separ.new \
			/etc/hotplug.d/iface/99-separ /lib/upgrade/keep.d/separ /usr/share/separ-uninstall.sh
	else
		say "the new panel did not answer; the old Separ files were kept"
	fi
fi

# One interface only: remove LuCI, but only after ToofanWrt is confirmed to be
# answering, so the router is never left without a web interface.
# KEEP_LUCI=1 sh install.sh keeps it. It can be put back from the panel.
if [ "$KEEP_LUCI" != 1 ] && [ "$PM" = opkg ] && opkg list-installed | grep -qE '^(luci|uhttpd)'; then
	if panel_up; then
		say "removing LuCI (ToofanWrt is now the only interface)"
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
# Installed by the old (Separ) panel: its updater only removes the "separ"
# link, so free the extracted release once it has finished.
if [ -n "$SEPAR_UPDATE" ]; then
	( sleep 150; rm -rf /tmp/separ-update ) </dev/null >/dev/null 2>&1 &
fi
if [ -z "$TOOFAN_UPDATE" ] && [ -z "$SEPAR_UPDATE" ]; then
	HERE="$(pwd)"
	case "$HERE" in /tmp/*) cd / && rm -rf "$HERE" ;; esac
	[ -L /tmp/separ ] && rm -f /tmp/separ
	rm -f /tmp/toofanwrt-*.tar.gz /tmp/toofanwrt-update/*.tar.gz 2>/dev/null || true
	rm -rf /tmp/toofanwrt-update/toofanwrt 2>/dev/null || true
fi

LANIP="$(uci -q get network.lan.ipaddr | cut -d/ -f1)"
PORT="$(uci -q get toofanwrt.main.listen | cut -d, -f1 | sed 's/.*://')"
say "done!"
echo
if [ "${PORT:-80}" = 80 ]; then
	echo "  پنل:  http://${LANIP:-192.168.1.1}"
else
	echo "  پنل:  http://${LANIP:-192.168.1.1}:$PORT"
fi
echo "  رمز:  همان رمز root روتر (SSH)"
echo
