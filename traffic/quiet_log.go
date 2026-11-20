package traffic

import (
	"io"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

// benignMproxyLog filters the recording proxy's log stream. go-mitmproxy
// reports several expected disconnects at ERROR level from code paths that
// bypass its own normalErrMsgs filter: client-aborted requests and torn-down
// upstream sessions (msg=EOF), and targets that reset the TLS connection
// mid-handshake (connection reset by peer). These are normal against hostile
// or flaky targets, not recorder failures, and they drown the operator's real
// error signal. Error lines NOT matching these shapes pass through untouched.
type benignMproxyLog struct {
	next io.Writer
}

func (b *benignMproxyLog) Write(p []byte) (int, error) {
	line := string(p)
	if strings.Contains(line, "level=error") && strings.Contains(line, "in=Proxy.attacker") &&
		(strings.Contains(line, "msg=EOF") || strings.Contains(line, `: EOF"`) ||
			strings.Contains(line, "connection reset by peer") ||
			strings.Contains(line, "use of closed network connection")) {
		return len(p), nil
	}
	return b.next.Write(p)
}

// mproxy owns the process-global logrus logger (nothing else in this binary
// uses logrus), so pointing it at the filtered stream here covers the recorder
// and its TLS dialer.
func init() {
	log.SetOutput(&benignMproxyLog{next: os.Stderr})
}
