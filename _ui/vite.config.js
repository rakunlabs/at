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
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          'highlight': ['highlight.js'],
          'katex': ['katex'],
          'marked': ['marked'],
        }
      }
    }
  },
  server: {
    proxy: {
      '^/(api|gateway)/': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: true,
        ws: true,
        followRedirects: true
      }
    },
    port: 3000
  }
});
