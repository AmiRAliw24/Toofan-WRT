#!/bin/sh
# Removes the Separ panel and restores normal routing/DNS.
/etc/init.d/separ stop 2>/dev/null
/etc/init.d/separ disable 2>/dev/null
nft delete table inet separ 2>/dev/null
ip rule del fwmark 1 table 100 2>/dev/null
ip route flush table 100 2>/dev/null
for d in /tmp/dnsmasq.d /tmp/dnsmasq.*.d; do rm -f "$d/separ.conf"; done
/etc/init.d/dnsmasq restart
rm -f /usr/bin/separ /etc/init.d/separ /lib/upgrade/keep.d/separ /etc/hotplug.d/iface/99-separ /etc/config/separ
echo "Separ removed. Settings kept in /etc/separ (delete it with: rm -rf /etc/separ)"
