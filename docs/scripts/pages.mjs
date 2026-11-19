// Renders one static page per language from src/page.html and locales/*.json.
// English is served at the site root, every other language under its own path
// (de/, ja/, pt-br/ ...). The generated index.html files are build output and
// are not committed. Run `node scripts/pages.mjs --check` to validate locales.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const templatePath = join(siteRoot, 'src', 'page.html');
export const localesDir = join(siteRoot, 'locales');
const siteUrl = 'https://imariman.github.io/iapstack/';

// Order is the order of the language menu. `path` is the URL segment.
export const languages = [
  { code: 'en', path: '', name: 'English', short: 'EN', og: 'en_US' },
  { code: 'de', path: 'de/', name: 'Deutsch', short: 'DE', og: 'de_DE' },
  { code: 'es', path: 'es/', name: 'Español', short: 'ES', og: 'es_ES' },
  { code: 'fr', path: 'fr/', name: 'Français', short: 'FR', og: 'fr_FR' },
  { code: 'it', path: 'it/', name: 'Italiano', short: 'IT', og: 'it_IT' },
  { code: 'pt-BR', path: 'pt-br/', name: 'Português (Brasil)', short: 'PT', og: 'pt_BR' },
  { code: 'tr', path: 'tr/', name: 'Türkçe', short: 'TR', og: 'tr_TR' },
  { code: 'ru', path: 'ru/', name: 'Русский', short: 'RU', og: 'ru_RU' },
  { code: 'ja', path: 'ja/', name: '日本語', short: 'JA', og: 'ja_JP' },
  { code: 'ko', path: 'ko/', name: '한국어', short: 'KO', og: 'ko_KR' },
  { code: 'zh-CN', path: 'zh-cn/', name: '简体中文', short: '中文', og: 'zh_CN' },
];

const flatten = (object, prefix = '', out = {}) => {
  for (const [key, value] of Object.entries(object)) {
    const name = prefix ? `${prefix}.${key}` : key;
    if (value && typeof value === 'object') flatten(value, name, out);
    else out[name] = String(value);
  }
  return out;
};

const escapeHtml = (value) => value
  .replaceAll('&', '&amp;')
  .replaceAll('<', '&lt;')
  .replaceAll('>', '&gt;')
  .replaceAll('"', '&quot;');

const tagsOf = (value) => (value.match(/<\/?[a-z]+[^>]*>/gi) ?? []).map((tag) => tag.replace(/\s+[^>]*/, '').toLowerCase()).sort().join(' ');
const tokensOf = (value) => (value.match(/\{[a-z]+\}/g) ?? []).sort().join(' ');

const loadLocale = (code) => flatten(JSON.parse(readFileSync(join(localesDir, `${code}.json`), 'utf8')));

// Every locale must have exactly the English keys, the same markup in *_html
// values, the same {placeholders} in UI strings, and no markup elsewhere.
export const validateLocales = () => {
  const english = loadLocale('en');
  const problems = [];
  for (const language of languages) {
    if (!existsSync(join(localesDir, `${language.code}.json`))) {
      problems.push(`${language.code}: missing locales/${language.code}.json`);
      continue;
    }
    const strings = loadLocale(language.code);
    for (const key of Object.keys(english)) {
      if (!(key in strings)) problems.push(`${language.code}: missing ${key}`);
    }
    for (const [key, value] of Object.entries(strings)) {
      if (!(key in english)) { problems.push(`${language.code}: unknown key ${key}`); continue; }
      if (!value.trim()) problems.push(`${language.code}: empty ${key}`);
      if (key.endsWith('_html')) {
        if (tagsOf(value) !== tagsOf(english[key])) problems.push(`${language.code}: ${key} markup differs from English`);
      } else if (/[<>]/.test(value)) {
        problems.push(`${language.code}: ${key} contains markup; only *_html keys may`);
      }
      if (tokensOf(value) !== tokensOf(english[key])) problems.push(`${language.code}: ${key} placeholders differ from English`);
    }
  }
  return problems;
};

const relativeHref = (from, to) => {
  const depth = from.path ? from.path.split('/').filter(Boolean).length : 0;
  return `${'../'.repeat(depth)}${to.path}` || './';
};

const renderPage = (template, language) => {
  const strings = loadLocale(language.code);
  const ui = Object.fromEntries(Object.entries(strings).filter(([key]) => key.startsWith('ui.')).map(([key, value]) => [key.slice(3), value]));
  const alternates = languages.map((other) => `<link rel="alternate" hreflang="${other.code}" href="${siteUrl}${other.path}" />`)
    .concat(`<link rel="alternate" hreflang="x-default" href="${siteUrl}" />`)
    .join('\n    ');
  const menu = languages.map((other) => {
    const current = other.code === language.code ? ' aria-current="true"' : '';
    return `<li><a href="${relativeHref(language, other)}" hreflang="${other.code}" lang="${other.code}"${current}>${other.name}</a></li>`;
  }).join('');

  const values = {
    ...strings,
    '_lang': language.code,
    '_lang_name': language.name,
    '_lang_short': language.short,
    '_og_locale': language.og,
    '_url': `${siteUrl}${language.path}`,
    '_alternates': alternates,
    '_lang_menu': menu,
    '_ui_json': JSON.stringify(ui).replaceAll('<', '\\u003c'),
  };

  const html = template.replace(/\{\{([\w.]+)\}\}/g, (_, key) => {
    if (!(key in values)) throw new Error(`src/page.html uses unknown key {{${key}}}`);
    const value = values[key];
    return key.startsWith('_') || key.endsWith('_html') ? value : escapeHtml(value);
  });
  return `<!-- Generated by scripts/pages.mjs from src/page.html and locales/${language.code}.json. Do not edit. -->\n${html}`;
};

export const pageFile = (language) => join(siteRoot, language.path, 'index.html');

export const generatePages = () => {
  const problems = validateLocales();
  if (problems.length) throw new Error(`Locale check failed:\n  ${problems.join('\n  ')}`);
  const template = readFileSync(templatePath, 'utf8');
  for (const language of languages) {
    const file = pageFile(language);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, renderPage(template, language));
  }
  return languages.map(pageFile);
};

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (process.argv.includes('--check')) {
    const problems = validateLocales();
    if (problems.length) {
      console.error(`Locale check failed:\n  ${problems.join('\n  ')}`);
      process.exit(1);
    }
    console.log(`Locales OK: ${languages.map((language) => language.code).join(', ')}`);
  } else {
    console.log(generatePages().map((file) => file.replace(`${siteRoot}/`, '')).join('\n'));
  }
}
