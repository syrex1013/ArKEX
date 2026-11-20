package traffic

import (
	"testing"
)

func TestScannerDetection(t *testing.T) {
	cases := []struct {
		ua   string
		want bool
	}{
		{"sqlmap/1.7", true},
		{"Nikto/2.1.6", true},
		{"ffuf/1.5.0", true},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64)", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isScanner(tc.ua)
		if got != tc.want {
			t.Errorf("isScanner(%q) = %v want %v", tc.ua, got, tc.want)
		}
	}
}

func TestStaticMIMEFilter(t *testing.T) {
	cases := []struct {
		mime       string
		wantStatic bool
	}{
		{"image/png", true},
		{"font/woff2", true},
		{"audio/mpeg", true},
		{"application/json", false},
		{"text/html; charset=utf-8", false},
	}
	for _, tc := range cases {
		got := isStaticMIME(tc.mime)
		if got != tc.wantStatic {
			t.Errorf("isStaticMIME(%q) = %v want %v", tc.mime, got, tc.wantStatic)
		}
	}
}

func TestTextMIMEClassification(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{"application/json", true},
		{"text/html", true},
		{"application/xml", true},
		{"image/png", false},
		{"application/octet-stream", false},
	}
	for _, tc := range cases {
		got := isTextMIME(tc.mime)
		if got != tc.want {
			t.Errorf("isTextMIME(%q) = %v want %v", tc.mime, got, tc.want)
		}
	}
}
