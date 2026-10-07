import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

// The build lands in the Go server, which embeds it. In dev, /api goes to a running stackk-web.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../server/static/app', emptyOutDir: true },
  // localhost only: its proxy reaches stackk-web as the Mac itself, past -allow and -devices
  server: { host: '127.0.0.1', proxy: { '/api': 'http://127.0.0.1:7878' } },
})
