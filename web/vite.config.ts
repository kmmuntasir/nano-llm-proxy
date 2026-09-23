import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"

// Dev server proxies the Go backend so the GUI and /v1 share one origin
// (cookies stay first-party). Prod builds are embedded into the binary and
// served by the gateway itself, so no proxying there.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8787",
      "/v1": "http://127.0.0.1:8787",
      "/health": "http://127.0.0.1:8787",
    },
  },
})
