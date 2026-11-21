package traffic

import (
	"bytes"
	"testing"
)

// The recorder proxy logs expected disconnects at error level. They must be
// swallowed so the operator keeps the real error signal, while a genuine
// recorder error still reaches the log.
func TestBenignMproxyLogFiltersExpectedDisconnects(t *testing.T) {
	cases := []struct {
		line string
		keep bool
	}{
		{`time="2026-11-20T23:47:01Z" level=error msg="client disconnected" in=Proxy.attacker.attack method=GET url="https://h.example/obj"`, false},
		{`time="1" level=error msg="read: EOF" in=Proxy.attacker.attack method=GET url="https://h.example/"`, false},
		{`time="1" level=error msg="read tcp 1.2.3.4:1->5.6.7.8:443: connection reset by peer" in=Proxy.attacker.attack`, false},
		{`time="1" level=error msg="use of closed network connection" in=Proxy.attacker.attack`, false},
		{`time="1" level=error msg="WebSocket handle error: websocket: close 1005" in=Proxy.attacker.attack`, false},
		{`time="1" level=error msg="dial failed: no route to host" in=Proxy.attacker.attack`, true},
		{`time="1" level=warning msg="response body size >= 5242880\n" in=Proxy.attacker.attack`, false},
		{`time="1" level=info msg="Proxy start listen at :8788\n"`, true},
	}
	for _, c := range cases {
		var next bytes.Buffer
		n, err := (&benignMproxyLog{next: &next}).Write([]byte(c.line))
		if err != nil || n != len(c.line) {
			t.Fatalf("%q: write n=%d err=%v", c.line, n, err)
		}
		if kept := next.Len() > 0; kept != c.keep {
			t.Fatalf("%q: kept=%v want %v", c.line, kept, c.keep)
		}
	}
}
