package storage

import (
	"bytes"
	"strings"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	c := DefaultCodec()
	src := []byte(strings.Repeat("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"status\":\"ok\"}", 100))

	compressed, sha, err := c.Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("empty sha256")
	}
	if len(compressed) >= len(src) {
		t.Fatalf("no compression: %d >= %d", len(compressed), len(src))
	}

	got, err := c.Decompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestCodecCompressionRatio(t *testing.T) {
	c := DefaultCodec()
	// Compressible HTTP payload — expect ≥70% reduction
	src := []byte(strings.Repeat("GET /api/users HTTP/1.1\r\nHost: example.com\r\nAccept: application/json\r\n\r\n", 500))
	compressed, _, err := c.Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	ratio := float64(len(compressed)) / float64(len(src))
	if ratio > 0.30 {
		t.Fatalf("compression ratio too low: %.2f (want ≤0.30)", ratio)
	}
}

func BenchmarkCompress(b *testing.B) {
	c := DefaultCodec()
	src := bytes.Repeat([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"), 1000)
	b.ResetTimer()
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		if _, _, err := c.Compress(src); err != nil {
			b.Fatal(err)
		}
	}
}
