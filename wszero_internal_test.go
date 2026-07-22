package wszero

import (
	"encoding/binary"
	"net/http"
	"testing"
)

func xorMaskBytewise(b []byte, key32 uint32) {
	key := [4]byte{}
	binary.LittleEndian.PutUint32(key[:], key32)
	for i := range b {
		b[i] ^= key[i%4]
	}
}

func TestXorMask(t *testing.T) {
	t.Parallel()
	const key = uint32(0xDDCCBBAA)
	for sz := range 1024 {
		b0 := make([]byte, sz+wordSize-1)
		for algn := range wordSize {
			b := b0[algn:][:sz]
			xorMaskBytewise(b, key^0x12121212)
			xorMask(b, key)
			for i := range b {
				if b[i] != 0x12 {
					t.Errorf("size:%d, align:%d, offset:%d", sz, algn, i)
				}
				b[i] = 0
			}
		}
	}
}

func TestCheckSameOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin header", "example.com", "", true},
		{"empty origin value", "example.com", "EMPTY", false},
		{"same host", "example.com", "http://example.com", true},
		{"same host https", "example.com", "https://example.com", true},
		{"same host with port", "example.com:8080", "http://example.com:8080", true},
		{"case insensitive", "Example.COM", "http://example.com", true},
		{"case insensitive reversed", "example.com", "http://EXAMPLE.COM", true},
		{"different host", "example.com", "http://evil.com", false},
		{"different port", "example.com:8080", "http://example.com:9090", false},
		{"origin host with port vs no port", "example.com", "http://example.com:8080", false},
		{"no port vs origin port", "example.com:8080", "http://example.com", false},
		{"subdomain", "example.com", "http://sub.example.com", false},
		{"invalid origin url", "example.com", "://bad", false},
		{"empty host in origin", "example.com", "http://", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Host: tt.host, Header: http.Header{}}
			switch tt.origin {
			case "":
				// no Origin header
			case "EMPTY":
				r.Header.Set("Origin", "")
			default:
				r.Header.Set("Origin", tt.origin)
			}
			if got := checkSameOrigin(r); got != tt.want {
				t.Errorf("checkSameOrigin(%q, %q) = %v, want %v", tt.host, tt.origin, got, tt.want)
			}
		})
	}
}
