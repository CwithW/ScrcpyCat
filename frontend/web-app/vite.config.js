import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig(() => {
  const proxyTarget = process.env.VITE_PROXY_TARGET || 'http://localhost:8443'
  return {
    plugins: [vue()],
    resolve: {
      alias: {
        '@': resolve(__dirname, 'src')
      }
    },
    server: {
      port: 3000,
      proxy: Object.fromEntries([
        '/connect_client', '/register_agent', '/devices', '/agent',
        '/api', '/upload', '/downloads', '/snapshots', '/healthz'
      ].map(prefix => [prefix, {
        target: proxyTarget,
        ws: prefix === '/connect_client' || prefix === '/register_agent',
        secure: false,
        changeOrigin: true
      }]))
    },
    build: {
      outDir: resolve(__dirname, '../../backend/internal/webui/dist'),
      emptyOutDir: true
    }
  }
})
