package storage

import (
	"fmt"
	"strings"
	"testing"
)

// Phase 1 gate (b): 10k fuzzer 404s + 500 JSON responses compress ≥85%.
func TestTrafficCorpusCompressionRatio(t *testing.T) {
	c := DefaultCodec()
	var raw []byte
	for i := 0; i < 10000; i++ {
		raw = append(raw, []byte(fmt.Sprintf(
			"HTTP/1.1 404 Not Found\r\nServer: nginx\r\nContent-Type: text/html\r\nContent-Length: 48\r\n\r\n<html><body>404 %d not found</body></html>\n", i%50))...)
	}
	for i := 0; i < 500; i++ {
		raw = append(raw, []byte(fmt.Sprintf(
			`{"id":%d,"status":"ok","items":[{"name":"user","role":"admin"}],"meta":{"page":1,"total":100}}`, i%20))...)
	}
	compressed, _, err := c.Compress(raw)
	if err != nil {
		t.Fatal(err)
	}
	saved := 1 - float64(len(compressed))/float64(len(raw))
	if saved < 0.85 {
		t.Fatalf("compression saved %.1f%%, want ≥85%% (raw=%d compressed=%d)", saved*100, len(raw), len(compressed))
	}
}

// Phase 1 gate (a): delta transcript size stays under 5% of naive O(N²) replay.
func TestDeltaLLMRecordingSize(t *testing.T) {
	c := DefaultCodec()
	const turns = 100
	var msg string
	for i := 0; i < 20; i++ {
		msg += "Please analyze endpoint /api/v1/users and check for IDOR. "
	}

	var quadratic int
	var history []string
	for turn := 0; turn < turns; turn++ {
		history = append(history, fmt.Sprintf("user:%s #%d", msg, turn))
		history = append(history, fmt.Sprintf("assistant:observation %d "+strings.Repeat("x", 200), turn))
		// naive: re-serialize full history every turn
		blob := strings.Join(history, "\n")
		quadratic += len(blob)
	}

	var delta int
	for turn := 0; turn < turns; turn++ {
		user := fmt.Sprintf("user:%s #%d", msg, turn)
		asst := fmt.Sprintf("assistant:observation %d "+strings.Repeat("x", 200), turn)
		for _, part := range []string{user, asst} {
			comp, _, err := c.Compress([]byte(part))
			if err != nil {
				t.Fatal(err)
			}
			delta += len(comp)
		}
	}
	ratio := float64(delta) / float64(quadratic)
	if ratio > 0.05 {
		t.Fatalf("delta/quadratic = %.3f (delta=%d quadratic=%d), want <0.05", ratio, delta, quadratic)
	}
}
