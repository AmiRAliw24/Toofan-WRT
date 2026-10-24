package gaming

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestParseStunXor(t *testing.T) {
	// Binding success with XOR-MAPPED-ADDRESS 5.120.33.71:54321
	b := make([]byte, 32)
	binary.BigEndian.PutUint16(b[0:], 0x0101)
	binary.BigEndian.PutUint16(b[2:], 12)
	binary.BigEndian.PutUint32(b[4:], stunMagic)
	binary.BigEndian.PutUint16(b[20:], 0x0020)
	binary.BigEndian.PutUint16(b[22:], 8)
	b[25] = 0x01
	binary.BigEndian.PutUint16(b[26:], 54321^(stunMagic>>16))
	ip := net.IPv4(5, 120, 33, 71).To4()
	var m [4]byte
	binary.BigEndian.PutUint32(m[:], stunMagic)
	for i := 0; i < 4; i++ {
		b[28+i] = ip[i] ^ m[i]
	}
	a, err := parseStun(b)
	if err != nil || a.String() != "5.120.33.71:54321" {
		t.Fatal(a, err)
	}
}
