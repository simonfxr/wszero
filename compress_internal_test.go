package wszero

import (
	"compress/flate"
	"crypto/rand"
	"io"
	"testing"
)

func TestCompressDecompressRoundtrip(t *testing.T) {
	messages := [][]byte{
		[]byte("hello world"),
		[]byte("short"),
		{},
		[]byte("a longer message that should compress well because it repeats: hello hello hello hello"),
	}
	// Add a random data case
	rnd := make([]byte, 4096)
	io.ReadFull(rand.Reader, rnd)
	messages = append(messages, rnd)

	for _, level := range []int{flate.HuffmanOnly, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression} {
		for _, msg := range messages {
			compressed, err := compressMessage(msg, level, nil)
			if err != nil {
				t.Fatalf("compressMessage(level=%d, len=%d) failed: %v", level, len(msg), err)
			}

			decompressed, err := decompressMessage(compressed, 1<<20, nil)
			if err != nil {
				t.Fatalf("decompressMessage(level=%d, len=%d) failed: %v", level, len(msg), err)
			}
			if string(decompressed) != string(msg) {
				t.Fatalf("roundtrip failed: level=%d, len=%d, got len=%d", level, len(msg), len(decompressed))
			}
		}
	}
}

func TestCompressDecompressWithPool(t *testing.T) {
	bp := NewBufferPool()
	msg := []byte("pooled compression roundtrip test data that repeats repeats repeats")

	compressed, err := compressMessage(msg, DefaultCompressionLevel, bp)
	if err != nil {
		t.Fatal(err)
	}

	decompressed, err := decompressMessage(compressed, 1<<20, bp)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != string(msg) {
		t.Fatalf("roundtrip failed")
	}
	bp.PutBuffer(compressed)
	bp.PutBuffer(decompressed)
}

func TestDecompressReadLimit(t *testing.T) {
	// Compress a large message
	msg := make([]byte, 10000)
	for i := range msg {
		msg[i] = byte(i%26) + 'a'
	}

	compressed, err := compressMessage(msg, DefaultCompressionLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Try to decompress with a limit smaller than the decompressed output
	_, err = decompressMessage(compressed, 500, nil)
	if err != ErrReadLimit {
		t.Fatalf("expected ErrReadLimit, got %v", err)
	}
}

func TestNegotiateCompression(t *testing.T) {
	tests := []struct {
		headers map[string][]string
		want    bool
	}{
		{map[string][]string{"Sec-Websocket-Extensions": {"permessage-deflate"}}, true},
		{map[string][]string{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits"}}, true},
		{map[string][]string{"Sec-Websocket-Extensions": {"permessage-deflate; server_no_context_takeover; client_no_context_takeover"}}, true},
		{map[string][]string{"Sec-Websocket-Extensions": {"other-extension, permessage-deflate"}}, true},
		{map[string][]string{"Sec-Websocket-Extensions": {"other-extension"}}, false},
		{map[string][]string{}, false},
		{nil, false},
	}

	for _, tt := range tests {
		got := negotiateCompression(tt.headers)
		if got != tt.want {
			t.Errorf("negotiateCompression(%v) = %v, want %v", tt.headers, got, tt.want)
		}
	}
}
