import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Localhost only: the dev server and its API proxy never bind to the network.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8090',
      '/videos': 'http://127.0.0.1:8090',
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
  },
})
