import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 多入口：每插件一个独立小应用；base './' 适配网关子路径
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: {
    outDir: 'dist',
    rollupOptions: {
      input: {
        deploy: 'deploy.html',
        logs: 'logs.html',
        monitor: 'monitor.html',
        shell: 'shell.html',
      },
    },
  },
})
