import { matchesCatalogItem, readCatalogState, writeCatalogState } from './catalog-filters.js';

const header = document.querySelector('[data-header]');

function onScroll() {
  header?.classList.toggle('is-scrolled', window.scrollY > 8);
}
onScroll();
window.addEventListener('scroll', onScroll, { passive: true });

/* Mega menu */
const megaItems = [...document.querySelectorAll('[data-mega-item]')];
const desktop = window.matchMedia('(min-width: 961px) and (hover: hover)');

function closeAllMega(except) {
  for (const item of megaItems) {
    if (item === except) continue;
    item.querySelector('[data-mega-toggle]')?.setAttribute('aria-expanded', 'false');
    item.querySelector('[data-mega]')?.setAttribute('hidden', '');
  }
}

function setMega(item, open) {
  closeAllMega(open ? item : null);
  item.querySelector('[data-mega-toggle]')?.setAttribute('aria-expanded', String(open));
  item.querySelector('[data-mega]')?.toggleAttribute('hidden', !open);
}

for (const item of megaItems) {
  const toggle = item.querySelector('[data-mega-toggle]');
  let timer;

  toggle?.addEventListener('click', () => {
    setMega(item, toggle.getAttribute('aria-expanded') !== 'true');
  });
  item.querySelector('[data-mega-close]')?.addEventListener('click', () => {
    setMega(item, false);
    toggle?.focus();
  });
  item.addEventListener('mouseenter', () => {
    if (!desktop.matches) return;
    clearTimeout(timer);
    setMega(item, true);
  });
  item.addEventListener('mouseleave', () => {
    if (!desktop.matches) return;
    timer = setTimeout(() => setMega(item, false), 180);
  });
}
document.addEventListener('keydown', (event) => {
  if (event.key !== 'Escape') return;
  const open = megaItems.find((item) => !item.querySelector('[data-mega]')?.hasAttribute('hidden'));
  if (open) {
    setMega(open, false);
    open.querySelector('[data-mega-toggle]')?.focus();
  }
  setDrawer(false);
});

document.addEventListener('click', (event) => {
  if (!megaItems.some((item) => item.contains(event.target))) closeAllMega();
});

/* Mobile drawer */
const drawer = document.querySelector('[data-drawer]');
const drawerToggle = document.querySelector('[data-drawer-toggle]');

function setDrawer(open) {
  if (!drawer || !drawerToggle) return;
  drawer.toggleAttribute('hidden', !open);
  drawerToggle.setAttribute('aria-expanded', String(open));
  document.body.style.overflow = open ? 'hidden' : '';
}

drawerToggle?.addEventListener('click', () => {
  setDrawer(drawerToggle.getAttribute('aria-expanded') !== 'true');
});
drawer?.addEventListener('click', (event) => {
  if (event.target.closest('a')) setDrawer(false);
});

/* Catalog filters */
const catalog = document.querySelector('[data-catalog]');

if (catalog) {
  const form = catalog.querySelector('[data-filter-form]');
  const cards = [...catalog.querySelectorAll('[data-card]')].map((element) => ({
    element,
    type: element.dataset.type || '',
    scene: (element.dataset.scene || '').split(' '),
    nail: element.dataset.nail || '',
    search: element.dataset.search || '',
  }));
  const title = catalog.querySelector('[data-catalog-title]');
  const count = catalog.querySelector('[data-filter-count]');
  const empty = catalog.querySelector('[data-empty]');
  const search = catalog.querySelector('[data-catalog-search]');
  const resultCount = catalog.querySelector('[data-result-count]');
  const baseTitle = title?.textContent ?? '';
  const typeNames = JSON.parse(catalog.getAttribute('data-type-names') || '{}');

  function restore() {
    const state = readCatalogState(new URLSearchParams(window.location.search));
    for (const input of form.querySelectorAll('input[type="checkbox"]')) {
      input.checked = state[input.name].includes(input.value);
      if (input.checked) input.closest('details')?.setAttribute('open', '');
    }
    search.value = state.q;
    apply();
  }

  function selected(name) {
    return [...form.querySelectorAll(`input[name="${name}"]:checked`)].map((input) => input.value);
  }

  function apply(historyMode) {
    const state = { type: selected('type'), scene: selected('scene'), nail: selected('nail'), q: search.value.trim() };
    let visible = 0;

    for (const card of cards) {
      const match = matchesCatalogItem(card, state);
      card.element.hidden = !match;
      if (match) visible += 1;
    }

    const total = state.type.length + state.scene.length + state.nail.length + Number(Boolean(state.q));
    if (count) count.textContent = String(total);
    if (resultCount) resultCount.textContent = String(visible);
    if (empty) empty.hidden = visible > 0;

    const types = state.type;
    if (title) title.textContent = types.length === 1 ? typeNames[types[0]] || baseTitle : baseTitle;

    if (historyMode) {
      const url = new URL(window.location.href);
      url.search = writeCatalogState(state, url.search).toString();
      if (url.href !== window.location.href) {
        window.history[historyMode === 'push' ? 'pushState' : 'replaceState'](null, '', url);
      }
    }
  }

  form.addEventListener('submit', (event) => { event.preventDefault(); apply('push'); });
  form.addEventListener('change', () => apply('push'));
  form.addEventListener('reset', () => setTimeout(() => apply('push')));
  search.addEventListener('input', () => apply('replace'));
  window.addEventListener('popstate', restore);
  restore();
}

/* Product photo gallery */
for (const gallery of document.querySelectorAll('[data-gallery]')) {
  const thumbs = [...gallery.querySelectorAll('[data-gallery-thumb]')];
  const panels = [...gallery.querySelectorAll('[data-gallery-panel]')];
  function select(index) {
    panels.forEach((panel, position) => { panel.hidden = position !== index; });
    thumbs.forEach((thumb, position) => thumb.setAttribute('aria-pressed', String(position === index)));
  }
  thumbs.forEach((thumb, index) => {
    thumb.addEventListener('click', () => select(index));
    thumb.addEventListener('keydown', (event) => {
      if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
      event.preventDefault();
      const next = (index + (event.key === 'ArrowRight' ? 1 : -1) + thumbs.length) % thumbs.length;
      select(next);
      thumbs[next].focus();
    });
  });
}
