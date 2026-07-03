import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

// vite-plugin-pwa is loaded dynamically with a graceful fallback so a
// stale node_modules (operator ran `git pull` then `turbo build`
// without re-running `npm install` after a dep bump) doesn't hard-stop
// the deploy. Pre-3.1.36 the static `import { VitePWA } from
// "vite-plugin-pwa"` would throw ERR_MODULE_NOT_FOUND on the very
// first build after the v3.1.34 dep bump, blocking the entire SPA
// build. Now: missing plugin → console warning + non-PWA build
// (still works, just no service worker / install prompt). The next
// build with a fresh node_modules picks the plugin up automatically.
//
// PWA setup notes (when the plugin IS available):
//   - registerType: "autoUpdate" — service worker self-updates without
//     prompting; combined with skipWaiting we always serve the latest
//     deploy on next reload.
//   - scope + start_url + base = "/whm/" — the panel is served from a
//     sub-path, so the manifest scope must match or Chrome refuses
//     to honour it as installable.
//   - workbox.navigateFallback — when offline, ANY route under /whm/*
//     resolves to the cached index.html so the SPA shell still renders
//     and our OfflineOverlay can take over (instead of the browser's
//     blank "no internet" page).
//   - workbox.runtimeCaching for /api/v1/version + /api/v1/branding —
//     these are public, no-auth, and load on every page; caching them
//     means the panel still renders chrome correctly when offline.
//   - We deliberately do NOT cache authenticated /api/* responses —
//     stale dashboard data is worse than a clean offline state.
export default defineConfig(async () => {
  let pwaPlugin: any = null;
  try {
    const mod = await import("vite-plugin-pwa");
    pwaPlugin = mod.VitePWA({
      registerType: "autoUpdate",
      includeAssets: ["pwa-icon.svg"],
      manifest: {
        name: "Betazen Server Panel — WHM",
        short_name: "Betazen WHM",
        description:
          "Self-hosted WHM/cPanel-style server management — platform owner panel.",
        start_url: "/whm/",
        scope: "/whm/",
        display: "standalone",
        orientation: "any",
        background_color: "#0b1120",
        theme_color: "#0891b2",
        icons: [
          {
            src: "pwa-icon.svg",
            sizes: "any",
            type: "image/svg+xml",
            purpose: "any maskable",
          },
        ],
      },
      workbox: {
        // The WHM bundle crossed Workbox's default 2 MiB precache ceiling
        // (index chunk ~2.1 MB), which made `generateSW` throw and failed
        // the ENTIRE `vite build` (exit 1) — so every deploy silently kept
        // the stale WHM dist. Raise the ceiling to 5 MiB so the main chunk
        // precaches and the build passes; revisit with route-level code
        // splitting if the bundle keeps growing.
        maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        navigateFallback: "/whm/index.html",
        navigateFallbackDenylist: [/^\/api\//, /^\/webmail/, /^\/docs\//],
        runtimeCaching: [
          {
            urlPattern: /\/api\/v1\/(version|branding|public-settings)$/,
            handler: "StaleWhileRevalidate",
            options: {
              cacheName: "panel-public-meta",
              expiration: { maxAgeSeconds: 60 * 60 * 24 },
            },
          },
          {
            urlPattern: /\.(?:js|css|woff2?|svg|png|webp)$/,
            handler: "StaleWhileRevalidate",
            options: { cacheName: "panel-static-assets" },
          },
        ],
      },
      devOptions: {
        enabled: false,
      },
    });
  } catch (err) {
    console.warn(
      "[vite] vite-plugin-pwa not installed — building without PWA features. " +
        "Run `npm install` in /opt/serverpanel/frontend (or use `bzpanel deploy`) " +
        "to enable the service worker + install prompt."
    );
  }
  return {
    plugins: [react(), ...(pwaPlugin ? [pwaPlugin] : [])],
    base: "/whm/",
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      port: 3000,
      proxy: {
        "/api": "http://localhost:8080",
      },
    },
  };
});
