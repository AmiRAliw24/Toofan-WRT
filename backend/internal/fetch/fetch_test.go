package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestChecksums(t *testing.T) {
	sum := "b58887a21b5183d202d272bc6a07e8a056c258c7ecca302dff01cd4a282b47d3"
	if got := FindSHA256(sum + "  release/geosite.dat\n"); got != sum {
		t.Errorf("sha256sum line: %q", got)
	}
	dgst := "MD5= 0123\nSHA1= abcd\nSHA2-256= " + sum + "\nSHA2-512= " + sum + sum + "\nSHA3-256= 1111111111111111111111111111111111111111111111111111111111111111\n"
	if got := SHA256Line(dgst); got != sum {
		t.Errorf("dgst: %q", got)
	}
}

func TestToFileLimitAndRename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello world")) }))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "f")
	sum, _, err := ToFile(srv.URL, dst, 100, nil)
	if err != nil || sum != "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" {
		t.Fatalf("sum %q err %v", sum, err)
	}
	if _, _, err := ToFile(srv.URL, dst+"2", 5, nil); err == nil {
		t.Fatal("size cap not enforced")
	}
	if _, err := os.Stat(dst + "2.part"); !os.IsNotExist(err) {
		t.Fatal("partial file left behind")
	}
}
