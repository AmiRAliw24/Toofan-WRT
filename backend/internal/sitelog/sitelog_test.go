package sitelog

import "testing"

func TestRecordAndAggregate(t *testing.T) {
	l := New(func(ip string) string {
		if ip == "192.168.1.5" {
			return "aa:bb:cc:dd:ee:ff"
		}
		return ""
	})
	l.enabled = true
	for _, line := range []string{
		"Fri Oct 24 18:00:00 2026 daemon.info dnsmasq[1234]: query[A] www.google.com from 192.168.1.5",
		"Fri Oct 24 18:00:01 2026 daemon.info dnsmasq[1234]: 77 192.168.1.5/5353 query[AAAA] www.google.com from 192.168.1.5",
		"Fri Oct 24 18:00:02 2026 daemon.info dnsmasq[1234]: query[A] digikala.com. from 192.168.1.9",
		"Fri Oct 24 18:00:03 2026 daemon.info dnsmasq[1234]: query[A] router.lan from 192.168.1.5",
		"Fri Oct 24 18:00:04 2026 daemon.info dnsmasq[1234]: query[PTR] 1.1.168.192.in-addr.arpa from 192.168.1.5",
		"Fri Oct 24 18:00:05 2026 daemon.info dnsmasq[1234]: query[A] www.google.com from 127.0.0.1",
	} {
		if m := queryRe.FindStringSubmatch(line); m != nil {
			l.record(m[2], m[1], 100)
		}
	}
	dev := l.Sites("aa:bb:cc:dd:ee:ff")
	if len(dev) != 1 || dev[0].Domain != "www.google.com" || dev[0].Count != 2 {
		t.Fatalf("device sites = %+v", dev)
	}
	if s := l.Sites("ip:192.168.1.9"); len(s) != 1 || s[0].Domain != "digikala.com" {
		t.Fatalf("unknown-IP sites = %+v", s)
	}
	if all := l.Sites(""); len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
}
