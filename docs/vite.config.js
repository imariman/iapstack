import { defineConfig } from 'vite';
import { generatePages, localesDir, templatePath } from './scripts/pages.mjs';

// Every language is a static page rendered from src/page.html before Vite
// starts, so each one is a real build input with its own URL.
const pages = generatePages();

const localePages = () => ({
  name: 'iapstack-locale-pages',
  configureServer(server) {
    server.watcher.add([templatePath, localesDir]);
    server.watcher.on('change', (file) => {
      if (file !== templatePath && !file.startsWith(localesDir)) return;
      try {
        generatePages();
        server.ws.send({ type: 'full-reload' });
      } catch (error) {
        server.config.logger.error(error.message);
      }
    });
  },
});

export default defineConfig({
  base: process.env.GITHUB_ACTIONS ? '/iapstack/' : '/',
  plugins: [localePages()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: { input: pages },
  },
});
