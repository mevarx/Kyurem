import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

const apiOrigin = process.env.KYUREM_API_ORIGIN ?? "http://127.0.0.1:8082";
const apiProxy = {
  "/api": {
    target: apiOrigin,
    changeOrigin: true,
  },
};

// The dashboard talks to the Kyurem management API. In development the
// `/api` prefix is proxied to the gateway's management listener so that the
// browser sees a single origin (same as the compose stack does in production).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: apiProxy,
  },
  preview: {
    port: 4173,
    proxy: apiProxy,
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks: {
          react: ["react", "react-dom", "react-router-dom"],
          query: ["@tanstack/react-query"],
          charts: ["recharts"],
          graph: ["@xyflow/react", "dagre"],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: false,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    restoreMocks: true,
  },
});
