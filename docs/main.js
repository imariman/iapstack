const root = document.documentElement;

// Color theme: System follows the operating system, Light and Dark are an
// explicit choice. Same cycle and labels as the operations dashboard.
const themeStorageKey = 'iapstack-site-theme';
const themeOrder = ['system', 'light', 'dark'];
const themeLabels = { system: 'System', light: 'Light', dark: 'Dark' };
const themeBackgrounds = { light: '#fafafa', dark: '#09090b' };
const systemThemeQuery = window.matchMedia('(prefers-color-scheme: light)');

const readThemePreference = () => {
  try {
    const value = localStorage.getItem(themeStorageKey);
    return themeOrder.includes(value) ? value : 'system';
  } catch {
    return 'system';
  }
};

const writeThemePreference = (preference) => {
  try {
    if (preference === 'system') localStorage.removeItem(themeStorageKey);
    else localStorage.setItem(themeStorageKey, preference);
  } catch {
    // Storage can be unavailable (private mode); the choice then lasts for this visit.
  }
};

const resolvedTheme = (preference) => {
  if (preference !== 'system') return preference;
  return systemThemeQuery.matches ? 'light' : 'dark';
};

const applyTheme = (preference) => {
  if (preference === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', preference);

  const resolved = resolvedTheme(preference);
  document.querySelectorAll('meta[name="theme-color"]').forEach((meta) => {
    if (preference === 'system') {
      const lightMeta = meta.media.includes('light');
      meta.setAttribute('content', lightMeta ? themeBackgrounds.light : themeBackgrounds.dark);
    } else {
      meta.setAttribute('content', themeBackgrounds[resolved]);
    }
  });

  const next = themeOrder[(themeOrder.indexOf(preference) + 1) % themeOrder.length];
  document.querySelectorAll('[data-theme-toggle]').forEach((toggle) => {
    toggle.setAttribute('aria-label', `Color theme: ${themeLabels[preference]}. Switch to ${themeLabels[next]}`);
    toggle.setAttribute('title', `Theme: ${themeLabels[preference]}`);
    toggle.querySelectorAll('[data-theme-icon]').forEach((icon) => {
      icon.toggleAttribute('hidden', icon.dataset.themeIcon !== preference);
    });
  });
};

let themePreference = readThemePreference();
applyTheme(themePreference);

document.querySelectorAll('[data-theme-toggle]').forEach((toggle) => {
  toggle.addEventListener('click', () => {
    themePreference = themeOrder[(themeOrder.indexOf(themePreference) + 1) % themeOrder.length];
    writeThemePreference(themePreference);
    applyTheme(themePreference);
  });
});

systemThemeQuery.addEventListener('change', () => {
  if (themePreference === 'system') applyTheme(themePreference);
});

// Mobile menu
const header = document.querySelector('[data-header]');
const menuButton = document.querySelector('[data-menu-button]');
const mobileMenu = document.querySelector('[data-mobile-menu]');

const setMenu = (open) => {
  menuButton.setAttribute('aria-expanded', String(open));
  menuButton.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
  mobileMenu.hidden = !open;
};

menuButton.addEventListener('click', () => setMenu(menuButton.getAttribute('aria-expanded') !== 'true'));
mobileMenu.querySelectorAll('a').forEach((link) => link.addEventListener('click', () => setMenu(false)));
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && menuButton.getAttribute('aria-expanded') === 'true') {
    setMenu(false);
    menuButton.focus();
  }
});
window.matchMedia('(min-width: 861px)').addEventListener('change', (event) => {
  if (event.matches) setMenu(false);
});

const updateHeader = () => header.classList.toggle('is-scrolled', window.scrollY > 8);
window.addEventListener('scroll', updateHeader, { passive: true });
updateHeader();

// Copy quickstart commands
const toast = document.querySelector('[data-toast]');
document.querySelectorAll('[data-copy-target]').forEach((button) => {
  let resetTimer;
  button.addEventListener('click', async () => {
    const target = document.getElementById(button.dataset.copyTarget);
    const command = target.innerText
      .split('\n')
      .map((line) => line.trim().replace(/^\$\s*/, ''))
      .filter(Boolean)
      .join('\n');

    try {
      await navigator.clipboard.writeText(command);
    } catch {
      const textArea = document.createElement('textarea');
      textArea.value = command;
      textArea.style.position = 'fixed';
      textArea.style.opacity = '0';
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      textArea.remove();
    }

    const label = button.querySelector('span');
    const iconUse = button.querySelector('use');
    button.classList.add('is-copied');
    label.textContent = 'Copied';
    iconUse.setAttribute('href', '#i-check');
    toast.classList.add('is-visible');

    window.clearTimeout(resetTimer);
    resetTimer = window.setTimeout(() => {
      button.classList.remove('is-copied');
      label.textContent = 'Copy';
      iconUse.setAttribute('href', '#i-copy');
      toast.classList.remove('is-visible');
    }, 2200);
  });
});

// Reveal on scroll
if ('IntersectionObserver' in window) {
  const revealObserver = new IntersectionObserver((entries, observer) => {
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return;
      entry.target.classList.add('is-visible');
      observer.unobserve(entry.target);
    });
  }, { threshold: 0.1, rootMargin: '0px 0px -40px 0px' });

  document.querySelectorAll('.reveal').forEach((element, index) => {
    element.style.transitionDelay = `${(index % 3) * 50}ms`;
    revealObserver.observe(element);
  });

  const navigationLinks = document.querySelectorAll('.desktop-nav a[href^="#"]');
  const sectionObserver = new IntersectionObserver((entries) => {
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return;
      navigationLinks.forEach((link) => {
        const active = link.getAttribute('href') === `#${entry.target.id}`;
        link.classList.toggle('is-active', active);
        if (active) link.setAttribute('aria-current', 'location');
        else link.removeAttribute('aria-current');
      });
    });
  }, { rootMargin: '-35% 0px -60% 0px' });
  document.querySelectorAll('main section[id]').forEach((section) => sectionObserver.observe(section));
} else {
  document.querySelectorAll('.reveal').forEach((element) => element.classList.add('is-visible'));
}

// Demo verification chart. Synthetic, deterministic data shaped like the
// dashboard's 30-day activity chart, with the same hover tooltip.
const chart = document.querySelector('[data-chart]');
if (chart) {
  const plot = chart.querySelector('[data-chart-plot]');
  const barsHost = chart.querySelector('[data-chart-bars]');
  const tooltip = chart.querySelector('[data-chart-tooltip]');
  const live = chart.querySelector('[data-chart-live]');
  const dayFormat = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', timeZone: 'UTC' });
  const end = Date.UTC(2026, 10, 19);

  const days = Array.from({ length: 30 }, (_, index) => {
    const wave = Math.sin(index / 4.2) * 14 + Math.cos(index / 2.3) * 6;
    const allowed = Math.round(46 + index * 0.55 + wave);
    const denied = 2 + ((index * 7) % 5);
    const unresolved = (index * 3) % 4 === 0 ? 2 : (index % 3 === 0 ? 1 : 0);
    const date = new Date(end - (29 - index) * 86400000);
    return { allowed, denied, unresolved, total: allowed + denied + unresolved, label: dayFormat.format(date) };
  });
  const scale = 100;

  const bars = days.map((day) => {
    const bar = document.createElement('div');
    bar.className = 'chart-bar';
    [['allowed', day.allowed], ['denied', day.denied], ['unresolved', day.unresolved]].forEach(([kind, value]) => {
      if (!value) return;
      const segment = document.createElement('span');
      segment.className = `seg-${kind}`;
      segment.style.height = `${(value / scale) * 100}%`;
      bar.appendChild(segment);
    });
    barsHost.appendChild(bar);
    return bar;
  });

  let activeIndex = -1;
  const row = (kind, label, value) => `<dt><i class="swatch ${kind}"></i>${label}</dt><dd>${value}</dd>`;

  const show = (index) => {
    if (index === activeIndex) return;
    activeIndex = index;
    bars.forEach((bar, barIndex) => bar.classList.toggle('is-active', barIndex === index));
    barsHost.classList.add('has-active');
    const day = days[index];
    tooltip.innerHTML = `<strong>${day.label}</strong><span class="total">${day.total} verified</span><dl>${row('allowed', 'Allowed', day.allowed)}${row('denied', 'Denied', day.denied)}${row('unresolved', 'Unresolved', day.unresolved)}</dl>`;
    tooltip.hidden = false;

    const plotWidth = plot.clientWidth;
    const barCenter = bars[index].offsetLeft + bars[index].offsetWidth / 2;
    const tipWidth = tooltip.offsetWidth;
    const preferRight = barCenter + 14 + tipWidth <= plotWidth;
    const left = preferRight ? barCenter + 14 : Math.max(0, barCenter - 14 - tipWidth);
    tooltip.style.left = `${left}px`;
    live.textContent = `${day.label}: ${day.total} verified, ${day.allowed} allowed, ${day.denied} denied, ${day.unresolved} unresolved.`;
  };

  const hide = () => {
    activeIndex = -1;
    bars.forEach((bar) => bar.classList.remove('is-active'));
    barsHost.classList.remove('has-active');
    tooltip.hidden = true;
  };

  const indexFromPointer = (event) => {
    const rect = barsHost.getBoundingClientRect();
    const ratio = (event.clientX - rect.left) / rect.width;
    return Math.min(days.length - 1, Math.max(0, Math.floor(ratio * days.length)));
  };

  plot.addEventListener('pointermove', (event) => show(indexFromPointer(event)));
  plot.addEventListener('pointerdown', (event) => show(indexFromPointer(event)));
  plot.addEventListener('pointerleave', (event) => {
    if (event.pointerType === 'mouse') hide();
  });
  plot.addEventListener('focus', () => show(activeIndex >= 0 ? activeIndex : days.length - 1));
  plot.addEventListener('blur', hide);
  plot.addEventListener('keydown', (event) => {
    const moves = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };
    if (!(event.key in moves)) return;
    event.preventDefault();
    const step = moves[event.key];
    const current = activeIndex < 0 ? days.length - 1 : activeIndex;
    const next = Number.isFinite(step) ? current + step : (step < 0 ? 0 : days.length - 1);
    show(Math.min(days.length - 1, Math.max(0, next)));
  });
}

document.querySelector('[data-year]').textContent = new Date().getFullYear();
