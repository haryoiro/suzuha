import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Docker 内: API_URL=http://admin:8080
// ローカル: デフォルト http://localhost:8080
const apiTarget = process.env.API_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    watch: {
      // Docker bind mount ではファイル変更の inotify が届かないため polling を使用
      usePolling: true,
      interval: 500,
    },
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
  },
})
