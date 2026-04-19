import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// HTTPS はオフ: cloudflared が TLS 終端、localhost は browser が secure origin 扱い。
// basicSsl を入れると cloudflared が origin へ繋げなくなる。
const apiTarget = process.env.API_URL ?? "http://localhost:9090";
const wsTarget = apiTarget.replace(/^http/, "ws");

export default defineConfig({
  plugins: [react()],
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
