#!/bin/sh
# Removes the Separ panel and restores normal routing/DNS.
/etc/init.d/separ stop 2>/dev/null
/etc/init.d/separ disable 2>/dev/null
nft delete table inet separ 2>/dev/null
ip rule del fwmark 1 table 100 2>/dev/null
ip route flush table 100 2>/dev/null
for d in /tmp/dnsmasq.d /tmp/dnsmasq.*.d; do rm -f "$d/separ.conf"; done
/etc/init.d/dnsmasq restart
# Separ removed LuCI when it was installed; bring it back so the router keeps
# a web interface (needs internet).
if [ ! -x /etc/init.d/uhttpd ]; then
	echo "reinstalling LuCI..."
	opkg update >/dev/null 2>&1 && { opkg install luci || opkg install luci-light; } >/dev/null 2>&1 \
		|| echo "LuCI could not be downloaded; the router is still reachable over SSH. Later run: opkg update && opkg install luci"
fi
# Give port 80 back to LuCI.
if [ -x /etc/init.d/uhttpd ]; then
	uci -q delete uhttpd.main.listen_http
	uci add_list uhttpd.main.listen_http='0.0.0.0:80'
	uci add_list uhttpd.main.listen_http='[::]:80'
	uci -q delete uhttpd.main.listen_https
	uci add_list uhttpd.main.listen_https='0.0.0.0:443'
	uci add_list uhttpd.main.listen_https='[::]:443'
	uci commit uhttpd
	/etc/init.d/uhttpd enable
	/etc/init.d/uhttpd restart
fi
rm -rf /usr/libexec/separ
rm -f /usr/bin/separ /etc/init.d/separ /lib/upgrade/keep.d/separ /etc/hotplug.d/iface/99-separ /etc/config/separ
echo "Separ removed; LuCI is back on http://$(uci -q get network.lan.ipaddr | cut -d/ -f1)"
echo " Settings kept in /etc/separ (delete it with: rm -rf /etc/separ)"
