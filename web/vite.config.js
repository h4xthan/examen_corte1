import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // El API vive entero bajo /api (mismo prefijo que en producción detrás de
      // Netlify/nginx). F5 en rutas SPA como /admin ya no choca con ninguna
      // ruta de la API, así que el bypass que distinguía navegación de llamada
      // desapareció con él.
      '/api': 'http://localhost:8000',
    },
  },
})