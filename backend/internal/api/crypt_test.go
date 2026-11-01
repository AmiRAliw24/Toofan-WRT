package api

import (
	"os"
	"path/filepath"
	"testing"
)

// Vectors from openssl passwd -1/-5/-6 and OpenWrt 23.05's own passwd.
func TestVerifyCrypt(t *testing.T) {
	for _, v := range []struct{ pw, hash string }{
		{"", "$1$Akifnul7$T5Sby5tmwxImPEvuMY.3X0"},
		{"a", "$1$.18X.UdN$jXy6goKn3B493vCLRjMrr/"},
		{"a", "$5$TEZL9ucU5wntX60O$MjUXcI4iaPWTuFwHIVLIgu5rKV70/wdHHjfKjWTdnn2"},
		{"a", "$6$RuHgFUYN.7aMNNW6$a.acERGe972ljn0npPbAiCQYQ3/Eke13lhdHeaGMsEn3azaz9QI86SrAgnxjm4XxneD3eSVFEQt4Xanv8Cm/v0"},
		{"Pass word!123", "$1$d8VIEVPy$caMnwT7K8lgRq7npsKyvg."},
		{"Pass word!123", "$5$j.QsbS/28.5HebJZ$p4gCFEnVfmufovKiGMdQM/RY2jHgMbifPUmhsofuNUB"},
		{"Pass word!123", "$6$roIqYqW8fm0fuECa$TdEi3dXxKHl/eUQ2popk1MVVI6LJoUs/zglc7iSllMrpCuyea1uWbzzQXuTRBD8G6jeo3UQS40xZ0/tIZhmS7."},
		{"تست-رمز-خیلی-طولانی-برای-آزمایش-بیشتر-از-شصت-و-چهار-بایت", "$1$tIm2zs3s$L7KGaaDm6fsa5iOcEyzhJ1"},
		{"تست-رمز-خیلی-طولانی-برای-آزمایش-بیشتر-از-شصت-و-چهار-بایت", "$5$WipJwQWYawNTay9w$fie3X5lTo0OfmtM6Kpa4YLbdwRi4OB/XZ0M9e5ys5c."},
		{"تست-رمز-خیلی-طولانی-برای-آزمایش-بیشتر-از-شصت-و-چهار-بایت", "$6$ICMns09Eelyq3kf3$PP08fFykfMjdHnVZZBAFdTHRMs4xbQXXGipI0unC1bSsw80CXFwpNCVjhwE40o36YN.hEL1nb/KIm/0fo43eU0"},
		{"abc", "$5$rounds=1200$saltsalt$LcTuffhRZoqVsvInO6RtFLXRI6eOGvhwsNntn0QDru1"},
		{"Router#2026", "$1$sZelMepw$FAJzOPu7BNJDp6oB7yAnG0"},
	} {
		if m, ok := verifyCrypt(v.pw, v.hash); !ok || !m {
			t.Fatalf("%q should match %s (ok=%v)", v.pw, v.hash, ok)
		}
		if m, _ := verifyCrypt(v.pw+"x", v.hash); m {
			t.Fatalf("wrong password accepted for %s", v.hash)
		}
	}
	if _, ok := verifyCrypt("x", "$y$j9T$abc$def"); ok {
		t.Fatal("unknown formats must go to rpcd")
	}
}

func TestShadowLogin(t *testing.T) {
	dir := t.TempDir()
	shadowFile = filepath.Join(dir, "shadow")
	defer func() { shadowFile = "/etc/shadow" }()
	os.WriteFile(shadowFile, []byte("root:::0:99999:7:::\ndaemon:*:0:0:99999:7:::\n"), 0o600)
	if !rootHasNoPassword() {
		t.Fatal("empty root password not detected")
	}
	if m, ok := shadowCheck(""); !ok || !m {
		t.Fatal("empty password must log in when root has none")
	}
	if m, _ := shadowCheck("guess"); m {
		t.Fatal("any password accepted")
	}
	os.WriteFile(shadowFile, []byte("root:$1$sZelMepw$FAJzOPu7BNJDp6oB7yAnG0:20758:0:99999:7:::\n"), 0o600)
	if rootHasNoPassword() {
		t.Fatal("password set")
	}
	if m, ok := shadowCheck("Router#2026"); !ok || !m {
		t.Fatal("right password refused")
	}
	if m, ok := shadowCheck(""); !ok || m {
		t.Fatal("empty password accepted with a password set")
	}
}
