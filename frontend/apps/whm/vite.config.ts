import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { VitePWA } from "vite-plugin-pwa";
import path from "path";

// PWA setup notes:
//   - registerType: "autoUpdate" — service worker self-updates without
//     prompting the user; combined with skipWaiting we always serve
//     the latest deploy on next reload.
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
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
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
    }),
  ],
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
});
