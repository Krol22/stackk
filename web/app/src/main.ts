import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { applySize, applyTheme, currentSize, currentTheme } from './lib/theme'

applyTheme(currentTheme())
applySize(currentSize())

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app

// Offline shell and home-screen install (needs HTTPS: tailscale serve).
if ('serviceWorker' in navigator && location.protocol === 'https:') {
  navigator.serviceWorker.register('/sw.js')
}
