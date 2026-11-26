// Package wgproxy provides a userspace WireGuard client using wireguard-go's
// tun/netstack, exposing a local HTTP CONNECT + SOCKS5 proxy for task egress.
// No TUN device, no NET_ADMIN — works in unprivileged containers.
package wgproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

const (
	// DefaultPort is the default local proxy port.
	DefaultPort = 18101
	// MullvadDNS is the DNS server Mullvad uses for its WireGuard clients.
	MullvadDNS = "10.64.0.1"
)

// Session holds a running WireGuard netstack + proxy.
type Session struct {
	mu            sync.Mutex
	up            bool
	device        *device.Device
	tun           *netstack.Net
	listener      net.Listener
	proxy         *proxyServer
	port          uint16
	relay         string
	mode          string // "netstack" for userspace
	handshakeSeen bool   // a WireGuard handshake completed at least once
	failStreak    int    // consecutive tunnel dial failures (reset on success)
	cancel        context.CancelFunc
}

// failStreakMax is how many consecutive tunnel dial failures mark the session
// unhealthy (the relay went silent after a working handshake).
const failStreakMax = 5

// proxyServer serves HTTP CONNECT + SOCKS5 on a single listener.
type proxyServer struct {
	listener net.Listener
	port     uint16
}

// Config is the configuration for starting a WireGuard session.
type Config struct {
	PrivateKey string // WireGuard private key (base64)
	PeerKey    string // Peer public key (base64)
	Endpoint   string // Peer endpoint (IPv4:51820)
	ProxyPort  uint16 // Local proxy port (default 18101)
	DNS        string // DNS server (default MullvadDNS)
	LocalAddr  string // Tunnel interface IP (e.g. the Mullvad-assigned device address); default 10.0.0.2
}

// Start creates a userspace WireGuard device with netstack and starts the local proxy.
func Start(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.ProxyPort == 0 {
		cfg.ProxyPort = DefaultPort
	}

	// Parse private key
	key, err := base64.StdEncoding.DecodeString(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes, got %d", len(key))
	}

	// Parse peer key
	peerKey, err := base64.StdEncoding.DecodeString(cfg.PeerKey)
	if err != nil {
		return nil, fmt.Errorf("decode peer key: %w", err)
	}
	if len(peerKey) != 32 {
		return nil, fmt.Errorf("peer key must be 32 bytes, got %d", len(peerKey))
	}

	// Parse endpoint
	host, portStr, err := net.SplitHostPort(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("parse port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("endpoint must be IPv4, got %s", host)
	}

	// Build the netstack: local address (the relay only forwards packets from
	// the address assigned to this device — Mullvad enforces this), DNS via
	// Mullvad inside the tunnel.
	localIP := netip.MustParseAddr("10.0.0.2")
	if cfg.LocalAddr != "" {
		var err error
		localIP, err = netip.ParseAddr(cfg.LocalAddr)
		if err != nil || !localIP.Is4() {
			return nil, fmt.Errorf("parse local address %q: must be an IPv4 address", cfg.LocalAddr)
		}
	}
	dnsIP := netip.MustParseAddr(MullvadDNS)
	if cfg.DNS != "" {
		dnsIP, err = netip.ParseAddr(cfg.DNS)
		if err != nil {
			return nil, fmt.Errorf("parse DNS: %w", err)
		}
	}
	tunDevice, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{localIP},
		[]netip.Addr{dnsIP},
		1420,
	)
	if err != nil {
		return nil, fmt.Errorf("create netstack tun: %w", err)
	}

	logger := device.NewLogger(
		device.LogLevelError,
		fmt.Sprintf("wgproxy(%d)", cfg.ProxyPort),
	)
	wgDevice := device.NewDevice(tunDevice, conn.NewDefaultBind(), logger)

	// Configure via IPC: private key, peer, endpoint, default-route.
	if err := wgDevice.IpcSet(strings.Join([]string{
		"private_key=" + hexBytes(key),
		"public_key=" + hexBytes(peerKey),
		"endpoint=" + net.JoinHostPort(host, strconv.Itoa(port)),
		"allowed_ip=0.0.0.0/0",
		"allowed_ip=::/0",
		"persistent_keepalive_interval=25",
	}, "\n")); err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("configure wireguard device: %w", err)
	}
	if err := wgDevice.Up(); err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("bring up wireguard device: %w", err)
	}

	// Kick the first handshake immediately by sending one packet inside the
	// tunnel (DNS query to the Mullvad resolver). Without it the handshake
	// only starts once real traffic arrives, and callers cannot verify the
	// tunnel before accepting it.
	go func() {
		if c, derr := tnet.DialContext(context.Background(), "tcp", net.JoinHostPort(dnsIP.String(), "53")); derr == nil {
			c.Close()
		}
	}()

	// Start the local proxy listener before the device handles traffic.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.ProxyPort))
	if err != nil {
		wgDevice.Close()
		return nil, fmt.Errorf("listen on proxy port: %w", err)
	}

	proxy := &proxyServer{
		listener: listener,
		port:     cfg.ProxyPort,
	}

	// Session lifetime is owned by the session itself: dials must outlive the
	// caller's context (the watchdog starts with a 20s timeout; a request-scoped
	// Up context would kill every tunnelled connection the moment it returns).
	sctx, scancel := context.WithCancel(context.Background())
	_ = ctx

	sess := &Session{
		up:       true,
		device:   wgDevice,
		tun:      tnet,
		listener: listener,
		proxy:    proxy,
		port:     cfg.ProxyPort,
		relay:    host,
		mode:     "netstack",
		cancel:   scancel,
	}

	// Poll the device for a completed handshake so Healthy/WaitHandshake work.
	go func() {
		for {
			sess.mu.Lock()
			up, dev := sess.up, sess.device
			sess.mu.Unlock()
			if !up || dev == nil {
				return
			}
			if handshakeDone(dev) {
				sess.mu.Lock()
				sess.handshakeSeen = true
				sess.mu.Unlock()
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	go sess.serveProxy(sctx)
	return sess, nil
}

// hexBytes formats bytes as lowercase hex without separators (IPC format).
func hexBytes(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0xf]
	}
	return string(out)
}

// serveProxy accepts proxy connections until the listener closes.
func (s *Session) serveProxy(ctx context.Context) {
	for {
		c, err := s.proxy.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(ctx, c)
	}
}

// handleConn handles a single proxy connection (SOCKS5 or HTTP CONNECT).
func (s *Session) handleConn(ctx context.Context, c net.Conn) {
	defer c.Close()

	// Peek the first byte: 0x05 = SOCKS5 greeting, anything else tries HTTP CONNECT.
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil || n == 0 {
		return
	}

	if buf[0] == 0x05 {
		s.handleSOCKS5(ctx, c, buf[:n])
		return
	}
	fields := strings.Fields(firstLine(buf[:n]))
	if len(fields) >= 2 && strings.EqualFold(fields[0], "CONNECT") {
		s.handleHTTPConnect(ctx, c, buf[:n])
		return
	}
	s.handleHTTPForward(ctx, c, buf[:n])
}

// firstLine returns the text up to the first CRLF (or the whole input when no
// line break arrived yet).
func firstLine(b []byte) string {
	if i := bytes.Index(b, []byte("\r\n")); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// handleHTTPConnect parses "CONNECT host:port HTTP/1.x" and tunnels through the netstack.
func (s *Session) handleHTTPConnect(ctx context.Context, c net.Conn, head []byte) {
	req := string(head)
	lineEnd := strings.Index(req, "\r\n")
	if lineEnd < 0 {
		lineEnd = len(req)
	}
	fields := strings.Fields(req[:lineEnd])
	if len(fields) < 2 || !strings.EqualFold(fields[0], "CONNECT") {
		c.Write([]byte("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"))
		return
	}
	target, err := net.ResolveTCPAddr("tcp", fields[1])
	if err != nil || target.IP == nil {
		c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
		return
	}
	// DNS for names resolves inside the tunnel via the netstack resolver.
	up, err := s.tun.DialContext(ctx, "tcp", fields[1])
	if err != nil {
		s.dialFail()
		c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
		return
	}
	defer up.Close()
	s.dialOK()

	if _, err := c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	pumpBoth(c, up)
}

// handleHTTPForward serves plain-HTTP requests sent in absolute-form
// ("GET http://host/path HTTP/1.1"), the way proxy-aware clients forward
// http:// URLs. The request head is rewritten to origin-form, the origin is
// dialed through the netstack, and the exchange is pumped verbatim. Anything
// that is not an absolute-form http:// request (origin-form, absolute-form
// https://) is answered 400: https:// belongs in CONNECT, which the dispatch
// above routes first.
func (s *Session) handleHTTPForward(ctx context.Context, c net.Conn, head []byte) {
	head, ok := completeHead(c, head)
	if !ok {
		c.Write([]byte("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"))
		return
	}
	target, out, ok := parseForwardRequest(head)
	if !ok {
		c.Write([]byte("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"))
		return
	}
	up, err := s.tun.DialContext(ctx, "tcp", target)
	if err != nil {
		s.dialFail()
		c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
		return
	}
	defer up.Close()
	s.dialOK()
	if _, err := up.Write(out); err != nil {
		return
	}
	pumpBoth(c, up)
}

const (
	headTerm     = "\r\n\r\n"
	maxHeadBytes = 65536
)

// completeHead accumulates until the request head terminator is present, so a
// head split across reads (or larger than the first peek) still parses. The
// returned buffer includes any body bytes that arrived beyond the terminator.
func completeHead(c net.Conn, head []byte) ([]byte, bool) {
	if bytes.Contains(head, []byte(headTerm)) {
		return head, true
	}
	buf := make([]byte, 4096)
	for len(head) < maxHeadBytes {
		n, err := c.Read(buf)
		if n > 0 {
			head = append(head, buf[:n]...)
			if bytes.Contains(head, []byte(headTerm)) {
				return head, true
			}
		}
		if err != nil {
			return head, false
		}
	}
	return head, false
}

// parseForwardRequest rewrites an absolute-form HTTP request head into
// origin-form and returns the dial target host:port. Headers after the request
// line pass through unchanged; a missing Host header is synthesized from the
// authority. ok=false marks a request that must be answered 400.
func parseForwardRequest(head []byte) (target string, out []byte, ok bool) {
	end := bytes.Index(head, []byte(headTerm))
	if end < 0 {
		return "", nil, false
	}
	lineEnd := bytes.Index(head, []byte("\r\n"))
	if lineEnd < 0 || lineEnd > end {
		return "", nil, false
	}
	fields := strings.Fields(string(head[:lineEnd]))
	if len(fields) < 3 {
		return "", nil, false
	}
	u, err := url.Parse(fields[1])
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return "", nil, false
	}
	target = u.Host
	if u.Port() == "" {
		target = u.Host + ":80"
	}
	path := u.RequestURI()
	if path == "" || path[0] == '?' {
		path = "/" + path
	}
	rest := string(head[lineEnd+2:]) // headers, terminator, any body (request-line CRLF already re-emitted)
	hasHost := false
	for _, ln := range strings.Split(rest, "\r\n") {
		if strings.HasPrefix(strings.ToLower(ln), "host:") {
			hasHost = true
			break
		}
	}
	var b bytes.Buffer
	b.WriteString(fields[0])
	b.WriteByte(' ')
	b.WriteString(path)
	b.WriteByte(' ')
	b.WriteString(fields[2])
	b.WriteString("\r\n")
	if !hasHost {
		b.WriteString("Host: ")
		b.WriteString(u.Host)
		b.WriteString("\r\n")
	}
	b.WriteString(rest)
	return target, b.Bytes(), true
}

// handleSOCKS5 implements the no-auth SOCKS5 CONNECT flow.
func (s *Session) handleSOCKS5(ctx context.Context, c net.Conn, greeting []byte) {
	// Server choice: no auth required.
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	// Read the request: VER CMD RSV ATYP ADDR[...] PORT[2].
	head := make([]byte, 4)
	if _, err := readFull(c, head); err != nil {
		return
	}
	if head[1] != 0x01 { // only CONNECT
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch head[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := readFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := readFull(c, l); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := readFull(c, b); err != nil {
			return
		}
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := readFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	portB := make([]byte, 2)
	if _, err := readFull(c, portB); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(portB[0])<<8|int(portB[1])))

	up, err := s.tun.DialContext(ctx, "tcp", target)
	if err != nil {
		s.dialFail()
		c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer up.Close()
	s.dialOK()

	// BND.ADDR/BND.PORT are all-zero for a bound-and-connected reply.
	c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	pumpBoth(c, up)
}

// readFull reads exactly len(b) bytes.
func readFull(c net.Conn, b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := c.Read(b[total:])
		if n > 0 {
			total += n
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// pumpBoth copies data in both directions until either side closes.
func pumpBoth(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
}

// Stop shuts down the WireGuard device and proxy listener.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.up = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	var errs []error

	if s.device != nil {
		s.device.Close()
		s.device = nil
	}

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close listener: %w", err))
		}
		s.listener = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("stop errors: %v", errs)
	}
	return nil
}

// Status returns the current session status.
func (s *Session) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.up {
		return map[string]any{
			"up":       false,
			"mode":     "",
			"relay":    "",
			"endpoint": "",
		}
	}

	return map[string]any{
		"up":       true,
		"mode":     s.mode,
		"relay":    s.relay,
		"endpoint": fmt.Sprintf("127.0.0.1:%d", s.port),
	}
}

// ProxyURL returns the local proxy URL (http://127.0.0.1:port) when up.
func (s *Session) ProxyURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.up {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

// Healthy reports whether the tunnel carries traffic: a WireGuard handshake
// completed and no long streak of recent tunnel dial failures is open.
func (s *Session) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.up && s.handshakeSeen && s.failStreak < failStreakMax
}

// WaitHandshake blocks until the first WireGuard handshake completes or the
// timeout expires; false means the relay never answered (unreachable, or the
// key is not registered on the peer's account).
func (s *Session) WaitHandshake(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		seen, up := s.handshakeSeen, s.up
		s.mu.Unlock()
		if seen || !up {
			return seen
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// dialOK records one successful tunnel dial.
func (s *Session) dialOK() {
	s.mu.Lock()
	s.failStreak = 0
	s.mu.Unlock()
}

// dialFail records one failed tunnel dial.
func (s *Session) dialFail() {
	s.mu.Lock()
	s.failStreak++
	s.mu.Unlock()
}

// NewHealthyTestSession builds a session marked up and handshaken, without a
// real device. For server tests that need a "healthy session" state.
func NewHealthyTestSession(proxyPort uint16) *Session {
	return &Session{up: true, port: proxyPort, mode: "netstack", handshakeSeen: true, relay: "test"}
}

// handshakeDone reports whether the device has at least one completed
// WireGuard handshake (IpcGet exposes last_handshake_time_*).
func handshakeDone(dev *device.Device) bool {
	out, err := dev.IpcGet()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "last_handshake_time_sec=") {
			v, _ := strconv.ParseInt(strings.TrimPrefix(line, "last_handshake_time_sec="), 10, 64)
			return v > 0
		}
	}
	return false
}

// GenerateKey creates a new WireGuard key pair (private, public base64).
func GenerateKey() (privateKey, publicKey string, err error) {
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		return "", "", fmt.Errorf("generate private key: %w", err)
	}
	// Curve25519 scalar base multiplication, per the WireGuard spec.
	pub, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("derive public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(private),
		base64.StdEncoding.EncodeToString(pub),
		nil
}
