import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/rpc': 'http://127.0.0.1:8765',
      '/events': 'http://127.0.0.1:8765',
    },
  },
});
