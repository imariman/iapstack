# Website and README translations

The project website is published in English and ten other languages. Each language is a
static page with its own URL (`/`, `/de/`, `/ja/`, …), rendered at build time from one
template.

| Path | Purpose |
| --- | --- |
| `docs/src/page.html` | Page template. Text is referenced as `{{section.key}}`. |
| `docs/locales/<code>.json` | Strings for one language. `en.json` is the source of truth. |
| `docs/scripts/pages.mjs` | Renders `docs/index.html` and `docs/<path>/index.html` (not committed) and validates the locales. |
| `README.<code>.md` | Translated README at the repository root, covering the user-facing sections only. |

Languages: `en`, `de`, `es`, `fr`, `it`, `pt-BR`, `tr`, `ru`, `ja`, `ko`, `zh-CN`. The list,
URL paths and menu labels live in `docs/scripts/pages.mjs`.

## Changing website text

1. Edit `src/page.html` and `locales/en.json`.
2. Add or update the same key in every other `locales/<code>.json`.
3. Run `npm run check:i18n` in `docs/`. It fails when a key is missing or unknown, a value is
   empty, an `*_html` value has different markup from English, a `ui.*` string has different
   `{placeholders}`, or a plain value contains markup.

`npm run dev` and `npm run build` regenerate the pages and run the same check.

## Changing the README

`README.md` is authoritative. The translated READMEs mirror its header, scope, SDK roadmap,
quick start, deploy overview, design principles, documentation links, roadmap and license, and
link back to the English README for development and step-by-step deployment details. When one
of those sections changes, update the translations in the same pull request or open an issue
for the follow-up.
