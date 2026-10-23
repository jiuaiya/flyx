import ReactDOM from "react-dom/client";
import { BrowserRouter, HashRouter } from "react-router-dom";

import App from "./App.tsx";
import { IS_DEMO, installDemo } from "./demo";
import { DemoBanner } from "./demo/banner";
import { Provider } from "./provider.tsx";
import "@/styles/globals.css";

// A page chunk from the previous build is gone after an upgrade: reload
// to pick up the new build instead of showing a blank page. At most once
// a minute, so a chunk that is really missing doesn't loop.
window.addEventListener("vite:preloadError", (event) => {
  const key = "flvx_chunk_reload";

  try {
    const last = Number(sessionStorage.getItem(key) || 0);

    if (Date.now() - last < 60000) return;
    sessionStorage.setItem(key, String(Date.now()));
  } catch {
    return;
  }
  event.preventDefault();
  window.location.reload();
});

if (IS_DEMO) installDemo();

// Bypass the HTTP cache for the worker and its imports. The worker activates
// immediately and reloads old clients only after the new assets are ready.
if (!IS_DEMO && import.meta.env.PROD && "serviceWorker" in navigator) {
  void navigator.serviceWorker
    .register(`${import.meta.env.BASE_URL}sw.js`, { updateViaCache: "none" })
    .then((registration) => {
      let checking = false;
      const check = async () => {
        if (
          checking ||
          document.visibilityState !== "visible" ||
          !navigator.onLine
        )
          return;
        checking = true;
        try {
          await registration.update();
        } catch {
          // A temporarily unavailable server must not interrupt the dashboard.
        } finally {
          checking = false;
        }
      };

      void check();
      window.setInterval(() => void check(), 60_000);
      document.addEventListener("visibilitychange", () => void check());
      window.addEventListener("focus", () => void check());
      window.addEventListener("online", () => void check());
      window.addEventListener("pageshow", () => void check());
    })
    .catch(() => {
      // The panel also works when the browser cannot install a service worker.
    });
}

// The demo is static files on GitHub Pages: hash routes need no server.
const Router = IS_DEMO ? HashRouter : BrowserRouter;

ReactDOM.createRoot(document.getElementById("root")!).render(
  <Router>
    <Provider>
      <App />
      {IS_DEMO && <DemoBanner />}
    </Provider>
  </Router>,
);
