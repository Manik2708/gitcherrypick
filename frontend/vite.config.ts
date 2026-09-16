import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The client talks to the API through a same-origin prefix.
//
// Two reasons, and both are load-bearing rather than tidiness:
//
//   The API mounts no CORS middleware, so a browser calling it on another
//   origin is refused before the request is made. curl does not enforce that,
//   which is why it is easy to miss from a terminal.
//
//   The OAuth state cookie is SameSite=Lax. localhost:5173 → 127.0.0.1:8080 is
//   cross-SITE (different hosts), so the browser would withhold the cookie on
//   the callback and every sign-in would fail its state check.
//
// Proxying makes both problems disappear without touching the backend: the
// browser only ever sees localhost:5173, and the cookie is first-party.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.API_ORIGIN ?? "http://127.0.0.1:8080",
        changeOrigin: false,
        rewrite: (path) => path.replace(/^\/api/, ""),
        // The API scopes its OAuth state cookie to Path=/auth. Behind the
        // prefix the callback is /api/auth/..., which does not match, so the
        // browser would withhold the cookie and every sign-in would fail its
        // state check. Re-scope what the proxy passes through.
        cookiePathRewrite: { "*": "/api" },
      },
    },
  },
  test: { environment: "jsdom", globals: true, setupFiles: ["./src/test-setup.ts"] },
});
