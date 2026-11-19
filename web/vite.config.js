import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import {fileURLToPath,URL} from 'node:url'

// 多入口：每插件一个独立小应用；base './' 适配网关子路径
export default defineConfig({
  plugins: [vue(),tailwindcss(),{name:'isolated-delivery-editor',enforce:'post',generateBundle(_,bundle){const entry=bundle['delivery-editor.html'];if(entry){this.emitFile({type:'asset',fileName:'assets/delivery-editor.html',source:String(entry.source).replaceAll('./assets/','./')})}}}],
  resolve:{alias:{'@':fileURLToPath(new URL('./src',import.meta.url))}},
  base: './',
  css: { preprocessorOptions: { less: { javascriptEnabled: false, math: 'always' } } },
  build: {
    outDir: 'dist',
    manifest:true,
    rollupOptions: {
      input: {
        deploy: 'deploy.html',
        apicenter: 'api-center.html',
		workbench: 'workbench.html',
		research: 'research.html',
        devtools: 'devtools.html',
        logs: 'logs.html',
        monitor: 'monitor.html',
        shell: 'shell.html',
        deliveryEditor:'delivery-editor.html',
      },
    },
  },
})
