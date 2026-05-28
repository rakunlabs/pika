package servertls

import (
	"crypto/tls"
	"net"
	"strconv"
	"time"
)

type Policy struct {
	HTTPS     bool
	PlainHTTP bool
}

type PolicyFunc func() Policy

// OptionalListener accepts TLS and, when policy allows it, plaintext
// HTTP on the same TCP port. This lets Pika default to HTTPS while
// still allowing an operator to temporarily enable HTTP from the UI.
type OptionalListener struct {
	net.Listener
	tlsConfig *tls.Config
	policy    PolicyFunc
}

func NewOptionalListener(base net.Listener, tlsConfig *tls.Config, policy PolicyFunc) *OptionalListener {
	return &OptionalListener{Listener: base, tlsConfig: tlsConfig, policy: policy}
}

func (l *OptionalListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		accepted := l.acceptConn(conn)
		if accepted != nil {
			return accepted, nil
		}
	}
}

func (l *OptionalListener) acceptConn(conn net.Conn) net.Conn {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	first := []byte{0}
	if _, err := conn.Read(first); err != nil {
		_ = conn.Close()
		return nil
	}
	_ = conn.SetReadDeadline(time.Time{})

	policy := Policy{HTTPS: true}
	if l.policy != nil {
		policy = l.policy()
	}
	peeked := &peekConn{Conn: conn, buf: first}
	if isTLSFirstByte(first[0]) {
		if !policy.HTTPS {
			_ = conn.Close()
			return nil
		}
		return tls.Server(peeked, l.tlsConfig)
	}
	if policy.PlainHTTP {
		return peeked
	}
	writeHTTPSRequired(conn)
	_ = conn.Close()
	return nil
}

func isTLSFirstByte(b byte) bool {
	// TLS records start with 0x16 for a handshake record. That is enough
	// to distinguish normal HTTP methods (G/P/H/...) for this listener.
	return b == 0x16
}

func writeHTTPSRequired(conn net.Conn) {
	const body = "HTTPS is required for this Pika listener. Enable plaintext HTTP in Settings only for trusted networks.\n"
	_, _ = conn.Write([]byte("HTTP/1.1 426 Upgrade Required\r\n" +
		"Connection: close\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body))
}

type peekConn struct {
	net.Conn
	buf []byte
}

func (c *peekConn) Read(p []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
