package runtime

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
)

// openListeners binds the HTTP port. The first listener is always on loopback
// (or the wildcard, which includes it), and its address is the one printed on
// LISTENING and handed to Claude Code for the MCP callback: those callers are
// on this host, and /mcp refuses anything that is not loopback.
//
// host is Options.ListenHost. Empty or 127.0.0.1 is the local-only default.
// A wildcard (0.0.0.0, ::) is one listener on every interface. Any other
// address is a second listener on that address, same port, served by the same
// app through mergeListeners.
func openListeners(host string, port int) (net.Listener, string, error) {
	loopback, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if host == "" || host == "127.0.0.1" {
		if err != nil {
			return nil, "", err
		}
		return loopback, loopback.Addr().String(), nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if loopback != nil {
			_ = loopback.Close()
		}
		return nil, "", fmt.Errorf("LISTEN_HOST %q is not an IP address", host)
	}
	if ip.IsUnspecified() {
		// The wildcard covers loopback and would collide with the listener
		// just opened, so it replaces it.
		if loopback != nil {
			_ = loopback.Close()
		}
		all, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return nil, "", err
		}
		_, p, _ := net.SplitHostPort(all.Addr().String())
		return all, net.JoinHostPort("127.0.0.1", p), nil
	}
	if err != nil {
		return nil, "", err
	}
	_, p, _ := net.SplitHostPort(loopback.Addr().String())
	extra, err := net.Listen("tcp", net.JoinHostPort(host, p))
	if err != nil {
		_ = loopback.Close()
		return nil, "", fmt.Errorf("listen on LISTEN_HOST %s: %w", host, err)
	}
	return mergeListeners(loopback, extra), loopback.Addr().String(), nil
}

// mergedListener serves several listeners through one Accept, so a single
// fiber app (whose Listener runs its startup once per call) serves them all,
// and one Close — the server's shutdown — closes every one.
type mergedListener struct {
	primary net.Listener
	all     []net.Listener
	conns   chan net.Conn
	done    chan struct{}
	once    sync.Once
}

func mergeListeners(primary net.Listener, others ...net.Listener) net.Listener {
	m := &mergedListener{
		primary: primary,
		all:     append([]net.Listener{primary}, others...),
		conns:   make(chan net.Conn),
		done:    make(chan struct{}),
	}
	for _, ln := range m.all {
		go m.pump(ln)
	}
	return m
}

func (m *mergedListener) pump(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		select {
		case m.conns <- c:
		case <-m.done:
			_ = c.Close()
			return
		}
	}
}

func (m *mergedListener) Accept() (net.Conn, error) {
	select {
	case c := <-m.conns:
		return c, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}

func (m *mergedListener) Close() error {
	var err error
	m.once.Do(func() {
		close(m.done)
		for _, ln := range m.all {
			if cerr := ln.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	})
	return err
}

func (m *mergedListener) Addr() net.Addr { return m.primary.Addr() }
