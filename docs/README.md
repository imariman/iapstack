# IAPStack website

Static marketing site built with Vite and deployed to GitHub Pages.

## Local development

```sh
npm install
npm run dev
```

## Production build

```sh
npm run build
npm run preview
```

The Pages workflow publishes `docs/dist` on pushes to `main` that change the website. In the repository settings, set **Pages → Build and deployment → Source** to **GitHub Actions**.
