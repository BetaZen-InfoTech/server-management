import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [react()],
  base: '/mail/',
  resolve: {
    alias: {
      // Mirror tsconfig.json's "paths": { "@/*": ["src/*"] } so Rollup
      // resolves imports like "@/store/auth" during the production
      // build. tsconfig paths only inform tsc's type-checker; Vite
      // needs this independent alias.
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
})
