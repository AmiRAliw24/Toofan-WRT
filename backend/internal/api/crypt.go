package api

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"hash"
	"strconv"
	"strings"
)

// Root password check straight from /etc/shadow, so logging in does not
// depend on rpcd (it goes away with LuCI on some installs, and apk removes
// it as an orphan). Supports the hashes OpenWrt's passwd writes: MD5-crypt
// ($1$) and SHA-crypt ($5$, $6$).

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// verifyCrypt reports whether pw matches the crypt(3) hash; ok is false for
// formats it doesn't know (the caller then asks rpcd).
func verifyCrypt(pw, hashed string) (match, ok bool) {
	var got string
	switch {
	case strings.HasPrefix(hashed, "$1$"):
		salt := strings.SplitN(hashed[3:], "$", 2)[0]
		got = md5Crypt(pw, salt)
	case strings.HasPrefix(hashed, "$5$"), strings.HasPrefix(hashed, "$6$"):
		parts := strings.Split(hashed, "$") // "", id, [rounds=N], salt, hash
		if len(parts) < 4 {
			return false, false
		}
		rounds, explicit := 5000, false
		saltPart := parts[2]
		if strings.HasPrefix(saltPart, "rounds=") {
			n, err := strconv.Atoi(strings.TrimPrefix(saltPart, "rounds="))
			if err != nil || len(parts) < 5 {
				return false, false
			}
			rounds, explicit, saltPart = n, true, parts[3]
		}
		got = shaCrypt(parts[1] == "6", pw, saltPart, rounds, explicit)
	default:
		return false, false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(hashed)) == 1, true
}

func b64From24(b2, b1, b0 byte, n int) string {
	w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
	out := make([]byte, 0, n)
	for ; n > 0; n-- {
		out = append(out, cryptAlphabet[w&0x3f])
		w >>= 6
	}
	return string(out)
}

func md5Crypt(pw, salt string) string {
	if len(salt) > 8 {
		salt = salt[:8]
	}
	p, s := []byte(pw), []byte(salt)
	alt := md5.Sum(append(append(append([]byte{}, p...), s...), p...))
	h := md5.New()
	h.Write(p)
	h.Write([]byte("$1$"))
	h.Write(s)
	for i := len(p); i > 0; i -= 16 {
		h.Write(alt[:min(i, 16)])
	}
	for i := len(p); i > 0; i >>= 1 {
		if i&1 != 0 {
			h.Write([]byte{0})
		} else {
			h.Write(p[:1])
		}
	}
	sum := h.Sum(nil)
	for i := 0; i < 1000; i++ {
		h := md5.New()
		if i&1 != 0 {
			h.Write(p)
		} else {
			h.Write(sum)
		}
		if i%3 != 0 {
			h.Write(s)
		}
		if i%7 != 0 {
			h.Write(p)
		}
		if i&1 != 0 {
			h.Write(sum)
		} else {
			h.Write(p)
		}
		sum = h.Sum(nil)
	}
	out := "$1$" + salt + "$" +
		b64From24(sum[0], sum[6], sum[12], 4) + b64From24(sum[1], sum[7], sum[13], 4) +
		b64From24(sum[2], sum[8], sum[14], 4) + b64From24(sum[3], sum[9], sum[15], 4) +
		b64From24(sum[4], sum[10], sum[5], 4) + b64From24(0, 0, sum[11], 2)
	return out
}

func shaCrypt(is512 bool, pw, salt string, rounds int, explicit bool) string {
	newH := sha256.New
	id := "$5$"
	if is512 {
		newH, id = sha512.New, "$6$"
	}
	if len(salt) > 16 {
		salt = salt[:16]
	}
	if rounds < 1000 {
		rounds = 1000
	}
	if rounds > 999999999 {
		rounds = 999999999
	}
	p, s := []byte(pw), []byte(salt)
	sumOf := func(parts ...[]byte) []byte {
		h := newH()
		for _, x := range parts {
			h.Write(x)
		}
		return h.Sum(nil)
	}
	b := sumOf(p, s, p)
	size := len(b)
	a := newH()
	a.Write(p)
	a.Write(s)
	repeat := func(h hash.Hash, src []byte, n int) {
		for ; n > size; n -= size {
			h.Write(src)
		}
		h.Write(src[:n])
	}
	repeat(a, b, len(p))
	for i := len(p); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(b)
		} else {
			a.Write(p)
		}
	}
	da := a.Sum(nil)
	dp := newH()
	for range p {
		dp.Write(p)
	}
	pBytes := make([]byte, 0, len(p))
	dpSum := dp.Sum(nil)
	for n := len(p); n > 0; n -= size {
		pBytes = append(pBytes, dpSum[:min(n, size)]...)
	}
	ds := newH()
	for i := 0; i < 16+int(da[0]); i++ {
		ds.Write(s)
	}
	dsSum := ds.Sum(nil)
	sBytes := dsSum[:len(s)]
	c := da
	for i := 0; i < rounds; i++ {
		h := newH()
		if i&1 != 0 {
			h.Write(pBytes)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sBytes)
		}
		if i%7 != 0 {
			h.Write(pBytes)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pBytes)
		}
		c = h.Sum(nil)
	}
	var enc strings.Builder
	if is512 {
		order := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
		for _, o := range order {
			enc.WriteString(b64From24(c[o[0]], c[o[1]], c[o[2]], 4))
		}
		enc.WriteString(b64From24(0, 0, c[63], 2))
	} else {
		order := [][3]int{{0, 10, 20}, {21, 1, 11}, {12, 22, 2}, {3, 13, 23}, {24, 4, 14}, {15, 25, 5}, {6, 16, 26}, {27, 7, 17}, {18, 28, 8}, {9, 19, 29}}
		for _, o := range order {
			enc.WriteString(b64From24(c[o[0]], c[o[1]], c[o[2]], 4))
		}
		enc.WriteString(b64From24(0, c[31], c[30], 3))
	}
	prefix := id
	if explicit {
		prefix += "rounds=" + strconv.Itoa(rounds) + "$"
	}
	return prefix + salt + "$" + enc.String()
}
