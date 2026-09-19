package runtime

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func serveHello(t *testing.T, ln net.Listener) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
}

func get(t *testing.T, addr string) string {
	t.Helper()
	c := http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestOpenListenersDefaultIsLoopbackOnly(t *testing.T) {
	ln, addr, err := openListeners("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !strings.HasPrefix(addr, "127.0.0.1:") || ln.Addr().String() != addr {
		t.Fatalf("addr %s / %s", addr, ln.Addr())
	}
}

func TestOpenListenersServesAnExtraHostOnTheSamePort(t *testing.T) {
	ln, addr, err := openListeners("127.0.0.2", 0)
	if err != nil {
		t.Skipf("127.0.0.2 not bindable here: %v", err)
	}
	serveHello(t, ln)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("the published address must stay on loopback: %s", addr)
	}
	_, port, _ := net.SplitHostPort(addr)
	if got := get(t, addr); got != "hello" {
		t.Fatalf("loopback: %s", got)
	}
	if got := get(t, net.JoinHostPort("127.0.0.2", port)); got != "hello" {
		t.Fatalf("extra host: %s", got)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if got := get(t, net.JoinHostPort("127.0.0.2", port)); !strings.HasPrefix(got, "error") {
		t.Fatalf("closing must close every listener, extra host answered: %s", got)
	}
	if got := get(t, addr); !strings.HasPrefix(got, "error") {
		t.Fatalf("closing must close every listener, loopback answered: %s", got)
	}
}

func TestOpenListenersWildcardPublishesLoopback(t *testing.T) {
	ln, addr, err := openListeners("0.0.0.0", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serveHello(t, ln)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("addr %s", addr)
	}
	if got := get(t, addr); got != "hello" {
		t.Fatalf("got %s", got)
	}
}

func TestOpenListenersRejectsANonIP(t *testing.T) {
	if _, _, err := openListeners("homeserver", 0); err == nil {
		t.Fatal("expected an error")
	}
}
