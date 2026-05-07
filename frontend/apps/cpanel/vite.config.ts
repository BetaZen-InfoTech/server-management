import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

// vite-plugin-pwa is loaded dynamically with a graceful fallback so a
// stale node_modules doesn't hard-stop the deploy. See the WHM
// vite.config.ts header for the full rationale.
//
// PWA setup mirrors the WHM app — see comments in apps/whm/vite.config.ts
// for the rationale on each option. Differences:
//   - scope + start_url + base = "/user-panel/" (the cpanel SPA's path).
//   - manifest name says "User Panel" so an installed user has both the
//     WHM and User Panel app icons distinguishable on their launcher.
export default defineConfig(async () => {
  let pwaPlugin: any = null;
  try {
    const mod = await import("vite-plugin-pwa");
    pwaPlugin = mod.VitePWA({
      registerType: "autoUpdate",
      includeAssets: ["pwa-icon.svg"],
      manifest: {
        name: "Betazen Server Panel — User Panel",
        short_name: "Betazen Panel",
        description:
          "Self-hosted WHM/cPanel-style server management — vendor / customer panel.",
        start_url: "/user-panel/",
        scope: "/user-panel/",
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
        navigateFallback: "/user-panel/index.html",
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
    // Vendor / customer panel is served at /user-panel/ — the historical
    // /cpanel/ prefix still 301-redirects to /user-panel/ in main.go for
    // one release so existing bookmarks don't break.
    base: "/user-panel/",
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: {
      port: 3001,
      proxy: {
        "/api": "http://localhost:8080",
      },
    },
  };
});
