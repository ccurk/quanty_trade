import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_BACKEND_URL || 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/ws': {
        target: process.env.VITE_BACKEND_URL || 'http://127.0.0.1:8080',
        ws: true,
        changeOrigin: true,
      },
    },
  },
})
