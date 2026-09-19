package http

import (
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/makifbaysal/tasktrooper/server/internal/application/webauth"
)

const (
	// The __Host- prefix makes the browser refuse the cookie unless it is
	// Secure, has Path=/ and no Domain — so no sibling host can plant or read it.
	webSessionCookie = "__Host-tt_session"
	// insecureWebSessionCookie is the plain-http spelling (WEB_COOKIE_INSECURE):
	// the prefix is only valid on a Secure cookie.
	insecureWebSessionCookie = "tt_session"
	// webCSRFHeader must accompany every state-changing request authenticated
	// by the cookie. A cross-site form cannot set a custom header, and a
	// cross-site fetch that does triggers a preflight this server never grants.
	webCSRFHeader = "X-TaskTrooper-Web"
	webAuthPrefix = "/auth/"
)

func (h *Handler) registerWebAuthRoutes(app *fiber.App) {
	if h.webAuth == nil {
		return
	}
	app.Post("/auth/login", h.WebLogin)
	app.Post("/auth/logout", h.WebLogout)
	app.Get("/auth/me", h.WebMe)
}

type webLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) WebLogin(c *fiber.Ctx) error {
	if !hasWebCSRFHeader(c) {
		return csrfRejected(c)
	}
	var req webLoginRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	addr := clientAddr(c)
	token, sess, err := h.webAuth.Login(c.UserContext(), strings.TrimSpace(req.Username), req.Password, addr)
	var locked *webauth.LockedError
	switch {
	case errors.As(err, &locked):
		seconds := int(math.Ceil(locked.RetryAfter.Seconds()))
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":       errorDetail{Message: "too many failed sign-in attempts", Type: "locked"},
			"retry_after": seconds,
		})
	case errors.Is(err, webauth.ErrInvalidCredentials):
		log.Info().Str("addr", addr).Msg("web sign-in failed")
		return c.Status(fiber.StatusUnauthorized).JSON(errorResponse{
			Error: errorDetail{Message: "invalid username or password", Type: "invalid_credentials"},
		})
	case err != nil:
		return internalError(c, err)
	}
	log.Info().Str("user", sess.Username).Str("addr", addr).Msg("web sign-in")
	h.setWebSessionCookie(c, token, h.webAuth.SessionTTL())
	return c.JSON(fiber.Map{"username": sess.Username})
}

func (h *Handler) WebLogout(c *fiber.Ctx) error {
	if !hasWebCSRFHeader(c) {
		return csrfRejected(c)
	}
	if err := h.webAuth.Logout(c.UserContext(), c.Cookies(h.sessionCookieName())); err != nil {
		return internalError(c, err)
	}
	h.clearWebSessionCookie(c)
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) WebMe(c *fiber.Ctx) error {
	sess, err := h.webAuth.Authenticate(c.UserContext(), c.Cookies(h.sessionCookieName()))
	if err != nil {
		if !errors.Is(err, webauth.ErrUnauthenticated) {
			log.Warn().Err(err).Msg("web session lookup failed")
		}
		return unauthorized(c)
	}
	return c.JSON(fiber.Map{"username": sess.Username})
}

// authenticateWebSession is authMiddleware's second path, taken only when the
// request carries no Authorization header and web sign-in is configured.
func (h *Handler) authenticateWebSession(c *fiber.Ctx) error {
	token := c.Cookies(h.sessionCookieName())
	if token == "" {
		return unauthorized(c)
	}
	sess, err := h.webAuth.Authenticate(c.UserContext(), token)
	if err != nil {
		if !errors.Is(err, webauth.ErrUnauthenticated) {
			log.Warn().Err(err).Msg("web session lookup failed")
		}
		return unauthorized(c)
	}
	if !isSafeMethod(c.Method()) && !hasWebCSRFHeader(c) {
		return csrfRejected(c)
	}
	c.Locals("client_name", "web:"+sess.Username)
	c.Locals("web_user", sess.Username)
	return c.Next()
}

func (h *Handler) sessionCookieName() string {
	if h.webCookieInsecure {
		return insecureWebSessionCookie
	}
	return webSessionCookie
}

func (h *Handler) setWebSessionCookie(c *fiber.Ctx, token string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{
		Name:     h.sessionCookieName(),
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		Secure:   !h.webCookieInsecure,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func (h *Handler) clearWebSessionCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     h.sessionCookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   !h.webCookieInsecure,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func hasWebCSRFHeader(c *fiber.Ctx) bool {
	return c.Get(webCSRFHeader) == "1"
}

func csrfRejected(c *fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(errorResponse{
		Error: errorDetail{Message: "missing " + webCSRFHeader + " header", Type: "csrf_error"},
	})
}

func isSafeMethod(method string) bool {
	return method == fiber.MethodGet || method == fiber.MethodHead || method == fiber.MethodOptions
}

// clientAddr is the address a sign-in lockout is keyed on. CF-Connecting-IP
// is believed only from a loopback peer — cloudflared on this machine. With
// LISTEN_HOST the server also answers the network directly, and a client there
// could otherwise send a fresh header value on every attempt and never be
// locked out.
func clientAddr(c *fiber.Ctx) string {
	peer := c.IP()
	if ip := net.ParseIP(peer); ip == nil || !ip.IsLoopback() {
		return peer
	}
	if cf := strings.TrimSpace(c.Get("CF-Connecting-IP")); cf != "" {
		if ip := net.ParseIP(cf); ip != nil {
			return ip.String()
		}
	}
	return peer
}
