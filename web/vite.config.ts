import { defineConfig } from "vitest/config";

// The Go binary serves the built app and the API on :8080. `npm run dev` proxies the API
// to a printpipe started with -mcp=false.
export default defineConfig({
  server: {
    port: 5173,
    proxy: { "/api": { target: "http://localhost:8080", changeOrigin: false } },
  },
  build: {
    outDir: "dist",
    target: "es2022",
    chunkSizeWarningLimit: 1500,
  },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.test.ts"],
  },
});
