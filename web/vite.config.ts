import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Production build lands inside the Go module so `//go:embed all:webui/dist`
// picks it up on the next `go build`. Dev server runs on :5173 and proxies
// API calls back to the FlareX admin HTTP (default 127.0.0.1:9090).
export default defineConfig({
  base: "/ui/",
  plugins: [react()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "src") },
  },
  build: {
    outDir: path.resolve(__dirname, "../internal/admin/webui/dist"),
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 600,
  },
  server: {
    port: 5173,
    proxy: {
      "/status": "http://127.0.0.1:9090",
      "/accounts": "http://127.0.0.1:9090",
      "/config": "http://127.0.0.1:9090",
      "/tokens": "http://127.0.0.1:9090",
      "/metrics": "http://127.0.0.1:9090",
      "/workers": "http://127.0.0.1:9090",
      "/apikeys": "http://127.0.0.1:9090",
      "/health": "http://127.0.0.1:9090",
    },
  },
});
