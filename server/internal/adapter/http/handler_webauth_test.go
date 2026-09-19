package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"

	"github.com/makifbaysal/tasktrooper/server/internal/application/webauth"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type memWebSessions struct {
	mu   sync.Mutex
	rows map[string]domain.WebSession
}

func (m *memWebSessions) Create(_ context.Context, s domain.WebSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[s.TokenHash] = s
	return nil
}

func (m *memWebSessions) Get(_ context.Context, h string) (domain.WebSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.rows[h]; ok {
		return s, nil
	}
	return domain.WebSession{}, domain.ErrWebSessionNotFound
}

func (m *memWebSessions) Touch(context.Context, string, time.Time, time.Time) error { return nil }

func (m *memWebSessions) Delete(_ context.Context, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, h)
	return nil
}

func (m *memWebSessions) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

func newWebAuthTestApp(t *testing.T, withWebAuth bool) *fiber.App {
	t.Helper()
	h := &Handler{legacyAPIKey: "server-api-key"}
	if withWebAuth {
		hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-pass"), webauth.MinBcryptCost)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := webauth.NewService(webauth.Config{
			Users: []webauth.User{{Name: "alice", Hash: hash}},
		}, &memWebSessions{rows: map[string]domain.WebSession{}})
		if err != nil {
			t.Fatal(err)
		}
		h.webAuth = svc
	}
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(h.authMiddleware)
	ok := func(c *fiber.Ctx) error {
		name, _ := c.Locals("client_name").(string)
		return c.SendString("ok:" + name)
	}
	app.Get("/v1/things", ok)
	app.Post("/v1/things", ok)
	app.Get("/metrics", ok)
	h.registerWebAuthRoutes(app)
	return app
}

type webReq struct {
	method, path, body string
	cookie, bearer     string
	csrf               bool
	cfIP               string
}

func doWeb(t *testing.T, app *fiber.App, r webReq) *http.Response {
	t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req := httptest.NewRequest(r.method, r.path, body)
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.cookie != "" {
		req.Header.Set("Cookie", webSessionCookie+"="+r.cookie)
	}
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.csrf {
		req.Header.Set(webCSRFHeader, "1")
	}
	if r.cfIP != "" {
		req.Header.Set("CF-Connecting-IP", r.cfIP)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}
	return resp
}

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == webSessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func login(t *testing.T, app *fiber.App) string {
	t.Helper()
	resp := doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true,
		body: `{"username":"alice","password":"s3cret-pass"}`})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("login: status %d", resp.StatusCode)
	}
	return sessionCookie(t, resp).Value
}

func TestWebLoginSetsAHardenedCookie(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	resp := doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true,
		body: `{"username":"alice","password":"s3cret-pass"}`})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	c := sessionCookie(t, resp)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie not hardened: %+v", c)
	}
	if c.MaxAge != int(webauth.DefaultSessionTTL.Seconds()) {
		t.Errorf("max-age %d", c.MaxAge)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"username":"alice"`) {
		t.Errorf("body %s", b)
	}
}

func TestWebLoginRequiresTheCSRFHeader(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	resp := doWeb(t, app, webReq{method: "POST", path: "/auth/login",
		body: `{"username":"alice","password":"s3cret-pass"}`})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

func TestWebLoginRejectsWrongPassword(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	resp := doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true,
		body: `{"username":"alice","password":"nope"}`})
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("no cookie on a failed sign-in")
	}
}

func TestWebSessionAuthenticatesAPIRequests(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	token := login(t, app)

	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things", cookie: token}).StatusCode; s != 200 {
		t.Errorf("GET with cookie: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "POST", path: "/v1/things", cookie: token}).StatusCode; s != fiber.StatusForbidden {
		t.Errorf("POST with cookie, no CSRF header: %d, want 403", s)
	}
	resp := doWeb(t, app, webReq{method: "POST", path: "/v1/things", cookie: token, csrf: true})
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "ok:web:alice" {
		t.Errorf("POST with cookie and header: %d %s", resp.StatusCode, b)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things", cookie: "forged"}).StatusCode; s != 401 {
		t.Errorf("forged cookie: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things"}).StatusCode; s != 401 {
		t.Errorf("nothing: %d", s)
	}
}

func TestBearerStillWorksBesideWebAuth(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	if s := doWeb(t, app, webReq{method: "POST", path: "/v1/things", bearer: "server-api-key"}).StatusCode; s != 200 {
		t.Errorf("bearer POST needs no CSRF header: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things", bearer: "wrong", cookie: login(t, app)}).StatusCode; s != 401 {
		t.Errorf("a wrong bearer is not rescued by a cookie: %d", s)
	}
}

func TestWebMeAndLogout(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	token := login(t, app)

	resp := doWeb(t, app, webReq{method: "GET", path: "/auth/me", cookie: token})
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"alice"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, b)
	}

	resp = doWeb(t, app, webReq{method: "POST", path: "/auth/logout", cookie: token, csrf: true})
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if c := sessionCookie(t, resp); c.Value != "" || !c.Expires.Before(time.Now()) {
		t.Errorf("logout must clear the cookie: %+v", c)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/auth/me", cookie: token}).StatusCode; s != 401 {
		t.Errorf("me after logout: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things", cookie: token}).StatusCode; s != 401 {
		t.Errorf("api after logout: %d", s)
	}
}

func TestWebLoginLockoutIsPerClientAddress(t *testing.T) {
	app := newWebAuthTestApp(t, true)
	bad := `{"username":"alice","password":"wrong"}`
	var last *http.Response
	for i := 0; i < webauth.DefaultMaxFailures; i++ {
		last = doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true, body: bad, cfIP: "198.51.100.7"})
	}
	if last.StatusCode != fiber.StatusTooManyRequests || last.Header.Get("Retry-After") != "900" {
		t.Fatalf("fifth failure: %d retry-after %q", last.StatusCode, last.Header.Get("Retry-After"))
	}
	good := `{"username":"alice","password":"s3cret-pass"}`
	if s := doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true, body: good, cfIP: "198.51.100.7"}).StatusCode; s != 429 {
		t.Errorf("locked address with the right password: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "POST", path: "/auth/login", csrf: true, body: good, cfIP: "192.0.2.4"}).StatusCode; s != 200 {
		t.Errorf("another address: %d", s)
	}
}

func TestMetricsNeedAuthOnlyWithWebAuth(t *testing.T) {
	if s := doWeb(t, newWebAuthTestApp(t, false), webReq{method: "GET", path: "/metrics"}).StatusCode; s != 200 {
		t.Errorf("without web auth /metrics stays public: %d", s)
	}
	app := newWebAuthTestApp(t, true)
	if s := doWeb(t, app, webReq{method: "GET", path: "/metrics"}).StatusCode; s != 401 {
		t.Errorf("with web auth /metrics needs a credential: %d", s)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/metrics", bearer: "server-api-key"}).StatusCode; s != 200 {
		t.Errorf("bearer on /metrics: %d", s)
	}
}

func TestWebAuthRoutesAbsentWhenDisabled(t *testing.T) {
	app := newWebAuthTestApp(t, false)
	if s := doWeb(t, app, webReq{method: "GET", path: "/auth/me"}).StatusCode; s != 404 {
		t.Errorf("/auth/me without web auth: %d, want 404", s)
	}
	if s := doWeb(t, app, webReq{method: "GET", path: "/v1/things", cookie: "anything"}).StatusCode; s != 401 {
		t.Errorf("a cookie means nothing without web auth: %d", s)
	}
}

func TestAuthPathsAreServerPaths(t *testing.T) {
	if !isServerPath("/auth/login") || !isServerPath("/auth/me") {
		t.Error("the SPA fallback must not answer /auth/*")
	}
}
