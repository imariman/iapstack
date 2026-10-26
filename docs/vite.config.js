import { defineConfig } from 'vite';

export default defineConfig({
  base: process.env.GITHUB_ACTIONS ? '/iapstack/' : '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
});
