import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    port: 5173,
    // 开发时把 API 代理到本地服务端，避免跨域配置
    proxy: { '/api': 'http://localhost:8080' },
  },
  // 构建产物由 ith5-server 直接托管，因此不需要额外的 Node 运行时
  build: { outDir: 'dist' },
})
