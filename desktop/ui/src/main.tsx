import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "@/App";
import { ErrorBoundary } from "@/components/ErrorBoundary";
// Bundled rather than fetched from Google: the packaged app serves this SPA from
// app://tasktrooper and has to render with no network at all.
import "@fontsource-variable/inter";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@/styles/globals.css";

function main() {
  const rootEl = document.getElementById("root");
  if (!rootEl) return;

  // With no bearer token (desktop shell or VITE_API_KEY) the app is being served
  // to a browser by the server itself; WebSessionGate in App asks for a sign-in,
  // or shows the configuration error when the server has web sign-in off.
  createRoot(rootEl).render(
    <StrictMode>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </StrictMode>,
  );
}

main();
