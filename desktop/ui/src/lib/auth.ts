// The credential this UI sends to the server.
//
// Two ways to be authenticated:
//
//   bearer token — the desktop shell generated it on first run, keeps it in
//     the OS keychain, passes it to the server it spawns and states it here;
//     or, in browser development, `VITE_API_KEY`, which must match the
//     server's `SERVER_API_KEY`.
//   web session — no token at all: the server itself serves this app and the
//     person signs in (`/auth/login`). The session is an HttpOnly cookie this
//     code never sees; the browser sends it on every same-origin request.
//
// Read at call time, not captured: the shell's preload installs the marker
// before any of this app's code runs, but a live lookup keeps the browser case
// a plain `null` rather than a module-load-order question.
export function getApiToken(): string | null {
  const fromShell = typeof window === "undefined" ? undefined : window.__tasktrooperDesktop?.apiToken;
  if (fromShell) return fromShell;
  const fromEnv = import.meta.env.VITE_API_KEY as string | undefined;
  return fromEnv ? fromEnv : null;
}

export function isWebSessionMode(): boolean {
  return getApiToken() === null;
}

// Sent on every request in web-session mode. The server refuses a
// cookie-authenticated write without it: a cross-site form cannot set a header,
// and a cross-site fetch that tries needs a preflight the server never grants.
export const WEB_SESSION_HEADER = "X-TaskTrooper-Web";

// Dispatched on window when a request is refused for want of a session, so the
// gate can show the sign-in page instead of every screen failing on its own.
export const SESSION_EXPIRED_EVENT = "tasktrooper:session-expired";
