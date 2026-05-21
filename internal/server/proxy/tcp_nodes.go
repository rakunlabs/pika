package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

type rawTCPMWBuilder func(cfg json.RawMessage, svc ServiceDeps) (TCPMiddleware, error)

func adaptTCPMW(b rawTCPMWBuilder) TCPNodeBuilder {
	return func(cfg json.RawMessage, svc ServiceDeps, _ TCPBranchSet) (TCPMiddleware, error) {
		return b(cfg, svc)
	}
}

func DefaultTCPMiddlewares() map[string]NodeSpec {
	specs := []NodeSpec{
		{
			Kind:        KindMiddleware,
			Protocol:    ProtocolTCP,
			Subtype:     "tcp-ip-allowlist",
			Label:       "TCP IP allowlist",
			Description: "Accept TCP connections only from the configured source CIDRs.",
			BuildTCP:    adaptTCPMW(buildTCPIPAllowMW),
		},
		{
			Kind:        KindMiddleware,
			Protocol:    ProtocolTCP,
			Subtype:     "tcp-ip-denylist",
			Label:       "TCP IP denylist",
			Description: "Close TCP connections from the configured source CIDRs.",
			BuildTCP:    adaptTCPMW(buildTCPIPDenyMW),
		},
	}

	out := make(map[string]NodeSpec, len(specs))
	for _, s := range specs {
		out[s.Subtype] = s
	}
	return out
}

func DefaultTCPHandlers() map[string]NodeSpec {
	specs := []NodeSpec{
		{
			Kind:        KindHandler,
			Protocol:    ProtocolTCP,
			Subtype:     "tcp-forward",
			Label:       "TCP forward",
			Description: "Forward raw TCP connections to a TCP, Unix, or UDP upstream. Mirrors turna's TCP redirect middleware.",
			BuildTCP:    adaptTCPMW(buildTCPForwardHandler),
		},
	}

	out := make(map[string]NodeSpec, len(specs))
	for _, s := range specs {
		out[s.Subtype] = s
	}
	return out
}

type tcpIPListCfg struct {
	CIDRs       []string `json:"cidrs,omitempty"`
	SourceRange []string `json:"source_range,omitempty"`
}

func buildTCPIPAllowMW(raw json.RawMessage, _ ServiceDeps) (TCPMiddleware, error) {
	nets, err := parseTCPIPList(raw)
	if err != nil {
		return nil, err
	}
	return func(next TCPHandler) TCPHandler {
		return func(ctx context.Context, conn *net.TCPConn) error {
			ip, err := tcpRemoteIP(conn)
			if err != nil {
				return err
			}
			if !ipInNets(ip, nets) {
				return fmt.Errorf("tcp-ip-allowlist: %s is not allowed", ip.String())
			}
			return next(ctx, conn)
		}
	}, nil
}

func buildTCPIPDenyMW(raw json.RawMessage, _ ServiceDeps) (TCPMiddleware, error) {
	nets, err := parseTCPIPList(raw)
	if err != nil {
		return nil, err
	}
	return func(next TCPHandler) TCPHandler {
		return func(ctx context.Context, conn *net.TCPConn) error {
			ip, err := tcpRemoteIP(conn)
			if err != nil {
				return err
			}
			if ipInNets(ip, nets) {
				return fmt.Errorf("tcp-ip-denylist: %s is denied", ip.String())
			}
			return next(ctx, conn)
		}
	}, nil
}

func parseTCPIPList(raw json.RawMessage) ([]*net.IPNet, error) {
	var cfg tcpIPListCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("tcp ip list config: %w", err)
		}
	}
	cidrs := cfg.CIDRs
	if len(cidrs) == 0 {
		cidrs = cfg.SourceRange
	}
	return parseIPList(ipListCfg{CIDRs: cidrs})
}

func tcpRemoteIP(conn *net.TCPConn) (net.IP, error) {
	addr := conn.RemoteAddr()
	if addr == nil {
		return nil, errors.New("tcp remote address missing")
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("tcp remote address is not an IP: %s", host)
	}
	return ip, nil
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

type tcpForwardCfg struct {
	Address       string `json:"address"`
	Network       string `json:"network,omitempty"`
	DialTimeout   string `json:"dial_timeout,omitempty"`
	Buffer        int    `json:"buffer,omitempty"`
	DisableNagle  bool   `json:"disable_nagle,omitempty"`
	ProxyProtocol bool   `json:"proxy_protocol,omitempty"`
}

func buildTCPForwardHandler(raw json.RawMessage, _ ServiceDeps) (TCPMiddleware, error) {
	var cfg tcpForwardCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("tcp-forward config: %w", err)
		}
	}
	cfg.Address = strings.TrimSpace(cfg.Address)
	if cfg.Address == "" {
		return nil, errors.New("tcp-forward: address is required")
	}
	network := strings.TrimSpace(cfg.Network)
	if network == "" {
		network = "tcp"
	}
	if err := validateTCPForwardTarget(network, cfg.Address); err != nil {
		return nil, err
	}
	buffer := cfg.Buffer
	if buffer <= 0 {
		buffer = 0xffff
	}
	var timeout time.Duration
	if cfg.DialTimeout != "" {
		d, err := time.ParseDuration(cfg.DialTimeout)
		if err != nil {
			return nil, fmt.Errorf("tcp-forward: dial_timeout: %w", err)
		}
		timeout = d
	}
	proxyProtocol := cfg.ProxyProtocol && (network == "tcp" || network == "tcp4" || network == "tcp6")

	return func(_ TCPHandler) TCPHandler {
		return func(ctx context.Context, lconn *net.TCPConn) error {
			return runTCPForward(ctx, lconn, tcpForwardRuntime{
				network:       network,
				address:       cfg.Address,
				dialTimeout:   timeout,
				buffer:        buffer,
				disableNagle:  cfg.DisableNagle,
				proxyProtocol: proxyProtocol,
			})
		}
	}, nil
}

func validateTCPForwardTarget(network, address string) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
		_, err := net.ResolveTCPAddr(network, address)
		if err != nil {
			return fmt.Errorf("tcp-forward: address cannot resolve %s: %w", address, err)
		}
	case "unix", "unixpacket":
		_, err := net.ResolveUnixAddr(network, address)
		if err != nil {
			return fmt.Errorf("tcp-forward: address cannot resolve %s: %w", address, err)
		}
	case "udp", "udp4", "udp6":
		_, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			return fmt.Errorf("tcp-forward: address cannot resolve %s: %w", address, err)
		}
	default:
		return fmt.Errorf("tcp-forward: unsupported network %s", network)
	}
	return nil
}

type tcpForwardRuntime struct {
	network       string
	address       string
	dialTimeout   time.Duration
	buffer        int
	disableNagle  bool
	proxyProtocol bool
}

func runTCPForward(ctx context.Context, lconn *net.TCPConn, cfg tcpForwardRuntime) error {
	dialCtx := ctx
	var cancel context.CancelFunc
	if cfg.dialTimeout > 0 {
		dialCtx, cancel = context.WithTimeout(ctx, cfg.dialTimeout)
	} else {
		dialCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	rconn, err := (&net.Dialer{}).DialContext(dialCtx, cfg.network, cfg.address)
	if err != nil {
		return fmt.Errorf("tcp-forward: dial %s/%s: %w", cfg.network, cfg.address, err)
	}
	defer rconn.Close()

	if cfg.disableNagle {
		_ = lconn.SetNoDelay(true)
		if tcp, ok := rconn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
	}
	if cfg.proxyProtocol {
		if err := writeProxyProtocolV1(lconn, rconn); err != nil {
			return err
		}
	}

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	slog.Debug("tcp proxy connection opened", "from", lconn.RemoteAddr(), "to", rconn.RemoteAddr())

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		if err == nil || isBenignCopyErr(err) {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	copyOne := func(dst io.Writer, src io.Reader) {
		defer wg.Done()
		buf := make([]byte, cfg.buffer)
		_, err := io.CopyBuffer(dst, src, buf)
		setErr(err)
		stop()
		_ = lconn.Close()
		_ = rconn.Close()
	}

	wg.Add(2)
	go copyOne(rconn, lconn)
	go copyOne(lconn, rconn)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		_ = lconn.Close()
		_ = rconn.Close()
		<-done
	case <-done:
	}

	slog.Debug("tcp proxy connection closed", "from", lconn.RemoteAddr(), "to", rconn.RemoteAddr())
	return firstErr
}

func writeProxyProtocolV1(lconn *net.TCPConn, rconn net.Conn) error {
	src, ok := lconn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return errors.New("tcp-forward: PROXY protocol requires TCP client address")
	}
	dst, ok := rconn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return errors.New("tcp-forward: PROXY protocol requires TCP upstream address")
	}
	proto := "TCP6"
	if src.IP.To4() != nil {
		proto = "TCP4"
	}
	line := fmt.Sprintf("PROXY %s %s %s %d %d\r\n", proto, src.IP.String(), dst.IP.String(), src.Port, dst.Port)
	if _, err := rconn.Write([]byte(line)); err != nil {
		return fmt.Errorf("tcp-forward: write PROXY protocol header: %w", err)
	}
	return nil
}

func isBenignCopyErr(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}
