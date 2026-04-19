import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
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
});
