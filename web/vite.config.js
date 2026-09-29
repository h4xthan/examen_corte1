import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // F5 en /admin choca con la API: si es navegación del navegador
      // (Accept: text/html) servir la app, no proxear al backend.
      '/admin': {
        target: 'http://localhost:8000',
        bypass: (req) => {
          if (req.headers.accept?.includes('text/html')) return '/index.html'
        },
      },
      '/books': 'http://localhost:8000',
      '/auth': 'http://localhost:8000',
      '/users': 'http://localhost:8000',
      '/orders': 'http://localhost:8000',
      '/order-items': 'http://localhost:8000',
      '/checkout': 'http://localhost:8000',
      '/reviews': 'http://localhost:8000',
      '/coupons': 'http://localhost:8000',
      '/payment-methods': 'http://localhost:8000',
      '/addresses': 'http://localhost:8000',
      '/uploads': 'http://localhost:8000',
    },
  },
})