import type { Config } from "tailwindcss";

const preset: Partial<Config> = {
  theme: {
    extend: {
      colors: {
        // Sky-blue accent (matches the Mail Suite webmail) — re-themes every
        // brand-* usage across WHM + cPanel + the shared UI components at once.
        brand: {
          50: "#f0f9ff",
          100: "#e0f2fe",
          200: "#bae6fd",
          300: "#7dd3fc",
          400: "#38bdf8",
          500: "#0ea5e9",
          600: "#0284c7",
          700: "#0369a1",
          800: "#075985",
          900: "#0c4a6e",
          950: "#082f49",
        },
        // Dark slate surfaces, nudged a touch bluer to sit under the sky accent.
        panel: {
          bg: "#0b1220",
          surface: "#111c2e",
          border: "#243449",
          text: "#e2e8f0",
          muted: "#94a3b8",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "-apple-system", "sans-serif"],
        mono: ["JetBrains Mono", "Fira Code", "monospace"],
      },
      boxShadow: {
        glow: "0 8px 30px -10px rgba(14,165,233,.5)",
      },
    },
  },
};

export default preset;
