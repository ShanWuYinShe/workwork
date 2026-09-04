import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// https://vite.dev/config/
export default defineConfig({
  plugins: [svelte()],
  // 本地开发代理：SPA 的 /api 请求转发到后端（默认 compose 内 admin，
  // 也可用 VITE_API_PROXY 指向带认证的反代，如本地 auth-proxy）。
  server: {
    proxy: {
      '/api': {
        target: process.env.VITE_API_PROXY || 'http://127.0.0.1:8318',
        changeOrigin: true,
      },
    },
  },
})
