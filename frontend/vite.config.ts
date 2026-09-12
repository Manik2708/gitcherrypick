import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The dev server proxies the API under /api rather than the client calling
// 127.0.0.1:8080 directly.
//
// The API serves no Access-Control-* headers — it is not a browser-facing
// origin, and adding CORS to it is a backend decision that belongs to the
// Planner, not to this file. So the browser must never make a cross-origin
// request in the first place: /api is same-origin, Vite forwards it, and the
// preflight never happens.
//
// The prefix has to be distinct because the API and the client share paths.
// /auth/github/callback, /claims, /admin, /shortlists and /saved-searches are
// all routes on BOTH sides; proxying any of them by name would swallow the
// client route and break the OAuth return. /api collides with nothing, and the
// rewrite strips it back off before the request reaches the API.
//
// cookiePathRewrite is what makes the OAuth round-trip actually complete. The
// API scopes its state cookie to `Path=/auth`, which is correct when a browser
// talks to it directly — but through this proxy the browser is calling
// /api/auth/..., which does not match that path, so it would never send the
// cookie back and every callback would answer 400 invalid_state. Rewriting the
// path to / keeps the cookie in scope for the proxied requests.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
        cookiePathRewrite: { "*": "/" },
      },
    },
  },
  test: { environment: "jsdom", globals: true, setupFiles: ["./src/test-setup.ts"] },
});
