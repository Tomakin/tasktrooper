import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError, api } from "@/api";
import { Spinner } from "@/components/ui/spinner";
import { WebSessionContext } from "@/hooks/useWebSession";
import { isWebSessionMode, SESSION_EXPIRED_EVENT } from "@/lib/auth";
import { ConfigErrorPage } from "@/pages/ConfigErrorPage";
import { LoginPage } from "@/pages/LoginPage";

type GateState =
  | { kind: "checking" }
  | { kind: "signedOut"; expired: boolean }
  | { kind: "signedIn"; username: string }
  | { kind: "unavailable" };

/**
 * Renders the app only once there is a credential. In the desktop shell (or a
 * development build with VITE_API_KEY) that is the bearer token and this is a
 * pass-through. Otherwise the server is serving this app to a browser and the
 * person has to sign in; a server with web sign-in off answers /auth/me with
 * 404, which is the old "no API key" configuration error.
 */
export function WebSessionGate({ children }: { children: ReactNode }) {
  if (!isWebSessionMode()) return <>{children}</>;
  return <BrowserSessionGate>{children}</BrowserSessionGate>;
}

function BrowserSessionGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<GateState>({ kind: "checking" });

  useEffect(() => {
    let cancelled = false;
    api
      .webSession()
      .then((user) => !cancelled && setState({ kind: "signedIn", username: user.username }))
      .catch((e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 401) setState({ kind: "signedOut", expired: false });
        else setState({ kind: "unavailable" });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const onExpired = () =>
      setState((prev) => (prev.kind === "signedIn" ? { kind: "signedOut", expired: true } : prev));
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.webLogout();
    } finally {
      setState({ kind: "signedOut", expired: false });
    }
  }, []);

  const username = state.kind === "signedIn" ? state.username : "";
  const session = useMemo(() => ({ username, signOut }), [username, signOut]);

  switch (state.kind) {
    case "checking":
      return (
        <div className="flex h-screen items-center justify-center">
          <Spinner size="lg" />
        </div>
      );
    case "unavailable":
      return <ConfigErrorPage missing={["VITE_API_KEY", "WEB_AUTH_USERS"]} />;
    case "signedOut":
      return (
        <LoginPage
          sessionExpired={state.expired}
          onSignedIn={(user) => setState({ kind: "signedIn", username: user.username })}
        />
      );
    case "signedIn":
      return <WebSessionContext.Provider value={session}>{children}</WebSessionContext.Provider>;
  }
}
