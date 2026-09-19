import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, authHeaders } from "@/api";
import { WebSessionGate } from "@/components/auth/WebSessionGate";
import { SessionMenu } from "@/components/layout/SessionMenu";
import { I18nProvider } from "@/hooks/useI18n";
import { SESSION_EXPIRED_EVENT, WEB_SESSION_HEADER } from "@/lib/auth";
import type { TaskTrooperDesktopHost } from "@/lib/desktop-bridge";

function renderGate() {
  return render(
    <I18nProvider>
      <WebSessionGate>
        <p>the app</p>
        <SessionMenu />
      </WebSessionGate>
    </I18nProvider>,
  );
}

async function signIn(username: string, password: string) {
  fireEvent.change(screen.getByLabelText("Username"), { target: { value: username } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("WebSessionGate", () => {
  afterEach(() => {
    delete (window as { __tasktrooperDesktop?: TaskTrooperDesktopHost }).__tasktrooperDesktop;
  });

  it("is a pass-through when the desktop shell states a token", () => {
    (window as { __tasktrooperDesktop?: Partial<TaskTrooperDesktopHost> }).__tasktrooperDesktop = {
      apiToken: "desktop-token",
    };
    const me = vi.spyOn(api, "webSession");
    renderGate();
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sign out" })).toBeNull();
    expect(me).not.toHaveBeenCalled();
  });

  it("renders the app for a live session, with the user and a sign-out", async () => {
    vi.spyOn(api, "webSession").mockResolvedValue({ username: "alice" });
    const logout = vi.spyOn(api, "webLogout").mockResolvedValue(undefined);
    renderGate();
    expect(await screen.findByText("the app")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    expect(logout).toHaveBeenCalled();
    expect(screen.queryByText("the app")).toBeNull();
  });

  it("asks for a sign-in without a session and opens the app after one", async () => {
    vi.spyOn(api, "webSession").mockRejectedValue(new ApiError("no", 401));
    const login = vi.spyOn(api, "webLogin").mockResolvedValue({ username: "alice" });
    renderGate();
    await screen.findByLabelText("Username");
    await signIn("alice", "s3cret-pass");
    expect(await screen.findByText("the app")).toBeInTheDocument();
    expect(login).toHaveBeenCalledWith("alice", "s3cret-pass");
  });

  it("says why a sign-in failed", async () => {
    vi.spyOn(api, "webSession").mockRejectedValue(new ApiError("no", 401));
    const login = vi.spyOn(api, "webLogin").mockRejectedValue(new ApiError("bad", 401, "invalid_credentials"));
    renderGate();
    await screen.findByLabelText("Username");
    await signIn("alice", "wrong");
    expect(await screen.findByText("The username or password is incorrect.")).toBeInTheDocument();

    login.mockRejectedValue(new ApiError("locked", 429, "locked", 600));
    await signIn("alice", "wrong");
    expect(await screen.findByText("Too many failed attempts. Try again in 10 minutes.")).toBeInTheDocument();
  });

  it("returns to the sign-in page when a request finds the session gone", async () => {
    vi.spyOn(api, "webSession").mockResolvedValue({ username: "alice" });
    renderGate();
    await screen.findByText("the app");
    act(() => {
      window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
    });
    expect(await screen.findByText("Your session has ended. Please sign in again.")).toBeInTheDocument();
  });

  it("shows the configuration error when the server has web sign-in off", async () => {
    vi.spyOn(api, "webSession").mockRejectedValue(new ApiError("not found", 404));
    renderGate();
    await waitFor(() => expect(screen.getByText("WEB_AUTH_USERS", { exact: false })).toBeInTheDocument());
  });
});

describe("authHeaders", () => {
  afterEach(() => {
    delete (window as { __tasktrooperDesktop?: TaskTrooperDesktopHost }).__tasktrooperDesktop;
  });

  it("marks web-session requests and sends no bearer", () => {
    const headers = authHeaders({ "Content-Type": "application/json" });
    expect(headers[WEB_SESSION_HEADER]).toBe("1");
    expect(headers.Authorization).toBeUndefined();
  });

  it("sends the bearer, and no web marker, when a token exists", () => {
    (window as { __tasktrooperDesktop?: Partial<TaskTrooperDesktopHost> }).__tasktrooperDesktop = {
      apiToken: "desktop-token",
    };
    const headers = authHeaders();
    expect(headers.Authorization).toBe("Bearer desktop-token");
    expect(headers[WEB_SESSION_HEADER]).toBeUndefined();
  });
});

describe("request() in web-session mode", () => {
  it("announces an ended session on a 401, but not for the sign-in routes", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(
      async () => new Response(JSON.stringify({ error: { message: "no", type: "authentication_error" } }), { status: 401 }),
    );
    const onExpired = vi.fn();
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    try {
      await expect(api.getSettings()).rejects.toBeInstanceOf(ApiError);
      expect(onExpired).toHaveBeenCalledTimes(1);
      await expect(api.webSession()).rejects.toBeInstanceOf(ApiError);
      expect(onExpired).toHaveBeenCalledTimes(1);
      const init = fetchMock.mock.calls[0][1] as RequestInit;
      expect((init.headers as Record<string, string>)[WEB_SESSION_HEADER]).toBe("1");
    } finally {
      window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
    }
  });
});
