package traffic

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Scanner / static-asset noise filter (ArKex Phase 1).
// High-frequency scanner bursts and static MIME types skip full body storage
// and FTS indexing; metadata + body hash still record.

var scannerUAHints = []string{
	"ffuf", "feroxbuster", "nuclei", "sqlmap", "dirsearch", "gobuster",
	"wfuzz", "nikto", "zaproxy", "burp", "httpx", "katana",
}

type hostBurst struct {
	mu     sync.Mutex
	window time.Time
	count  int
}

var burstTrack sync.Map // host -> *hostBurst

func isScannerUA(ua string) bool {
	ua = strings.ToLower(ua)
	for _, h := range scannerUAHints {
		if strings.Contains(ua, h) {
			return true
		}
	}
	return false
}

func isStaticMIME(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	for _, p := range []string{"image/", "font/", "video/", "audio/"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	// Minified bundles: skip FTS (still may store body unless also scanner noise).
	if strings.Contains(ct, "javascript") || ct == "text/css" || ct == "application/javascript" {
		return true
	}
	return false
}

func hostBursting(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	v, _ := burstTrack.LoadOrStore(host, &hostBurst{})
	b := v.(*hostBurst)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.Sub(b.window) > time.Second {
		b.window = now
		b.count = 1
		return false
	}
	b.count++
	return b.count > 20
}

// noiseSkipBody reports whether this exchange should store metadata/hash only
// (no inline body, no FTS text).
func noiseSkipBody(reqHeader http.Header, host, respCT string, status int) bool {
	ua := ""
	if reqHeader != nil {
		ua = reqHeader.Get("User-Agent")
	}
	if isScannerUA(ua) {
		return true
	}
	if hostBursting(host) && (status == 404 || status == 403) {
		return true
	}
	_ = respCT
	_ = status
	return false
}

// Aliases for tests / callers.
func isScanner(ua string) bool { return isScannerUA(ua) }

func isTextMIME(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" {
		return false
	}
	if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") {
		return true
	}
	return false
}
