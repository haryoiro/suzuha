import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import basicSsl from "@vitejs/plugin-basic-ssl";

const apiTarget = process.env.API_URL ?? "http://localhost:9090";
const wsTarget = apiTarget.replace(/^http/, "ws");

export default defineConfig({
  plugins: [react(), basicSsl()],
  server: {
    port: 5174,
    host: true,
    allowedHosts: true,
    watch: { usePolling: true, interval: 500 },
    proxy: {
      "/ws/web": {
        target: wsTarget,
        ws: true,
        changeOrigin: true,
        rewriteWsOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
