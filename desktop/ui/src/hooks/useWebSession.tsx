import { createContext, useContext } from "react";

export interface WebSessionValue {
  username: string;
  signOut: () => Promise<void>;
}

export const WebSessionContext = createContext<WebSessionValue | null>(null);

/** The signed-in browser session, or null in the desktop shell (bearer token, no sign-in). */
export function useWebSession(): WebSessionValue | null {
  return useContext(WebSessionContext);
}
