import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { brandAssets } from './brand-assets.js';

export default defineConfig({
  base: './',
  plugins: [
    brandAssets(),
    tailwindcss(),
    svelte()
  ],
  resolve: {
    alias: {
      '@': '/src'
    }
  },
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: 'highlight', test: /node_modules[\\/](\.pnpm[\\/])?highlight\.js/ },
            { name: 'katex', test: /node_modules[\\/](\.pnpm[\\/])?katex/ },
            { name: 'marked', test: /node_modules[\\/](\.pnpm[\\/])?marked/ },
          ]
        }
      }
    }
  },
  server: {
    proxy: {
      '^/(api|gateway|auth)/': {
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
