import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  base: './',
  plugins: [
    tailwindcss(),
    svelte()
  ],
  resolve: {
    alias: {
      '@': '/src'
    }
  },
  server: {
    proxy: {
      '^/(api|data|raw|login)/': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: true,
        ws: true,
        followRedirects: true
      },
      '^/logout$': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: true
      }
    },
    port: 3000
  }
});
