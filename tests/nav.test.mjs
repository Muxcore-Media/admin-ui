import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const script = readFileSync(new URL('../assets/nav.js', import.meta.url), 'utf8');

function setup(t, desktop = false) {
  const dom = new JSDOM(`<!doctype html><html lang="en"><body>
    <a href="#main-content">Skip to main content</a>
    <aside id="sidebar" class="-translate-x-full">
      <a href="/" data-nav-link>Dashboard</a>
      <a href="/queue" data-nav-link>Queue</a>
      <a href="/logout">Log out</a>
    </aside>
    <div id="sidebar-overlay" class="hidden" aria-hidden="true"></div>
    <main id="main-content" hx-history-elt="true" tabindex="-1"><input id="filter"><div id="poll"></div></main>
    </body></html>`, { url: 'https://admin.example/', runScripts: 'outside-only' });
  const { window } = dom;
  t.after(() => window.close());
  const media = new window.EventTarget();
  media.matches = desktop;
  window.matchMedia = () => media;
  Object.defineProperty(window.document, 'readyState', { value: 'complete' });
  window.eval(script);
  const doc = window.document;
  return {
    doc,
    sidebar: doc.getElementById('sidebar'),
    main: doc.getElementById('main-content'),
    toggle: doc.querySelector('button[aria-controls="sidebar"]'),
    resize(desktop) {
      media.matches = desktop;
      media.dispatchEvent(new window.Event('change'));
    },
    key(key, shiftKey = false) {
      const event = new window.KeyboardEvent('keydown', { key, shiftKey, bubbles: true, cancelable: true });
      doc.activeElement.dispatchEvent(event);
      return event;
    },
    settle(target) {
      doc.dispatchEvent(new window.CustomEvent('htmx:afterSettle', { detail: { target } }));
    },
    navigate(path, history = false) {
      window.history.pushState({}, '', path);
      if (history) {
        doc.querySelector('[hx-history-elt]').innerHTML = '<h1>Restored page</h1>';
        doc.dispatchEvent(new window.CustomEvent('htmx:historyRestore'));
      }
      else this.settle(this.main);
    },
  };
}

test('closed mobile navigation is inert and excluded from the accessibility tree', t => {
  const { sidebar, main, toggle } = setup(t);
  assert(sidebar.hasAttribute('inert'));
  assert.equal(sidebar.getAttribute('aria-hidden'), 'true');
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  assert.equal(toggle.getAttribute('aria-label'), 'Open navigation menu');
  assert(!main.hasAttribute('inert'));
});

test('opening, tab cycling, Escape and focus restoration work without a mouse', t => {
  const { doc, sidebar, main, toggle, key } = setup(t);
  toggle.click();
  const first = sidebar.querySelector('a');
  assert.equal(doc.activeElement, first);
  assert(!sidebar.hasAttribute('inert'));
  assert(main.hasAttribute('inert'));
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert(key('Tab', true).defaultPrevented);
  assert.equal(doc.activeElement, toggle);
  assert(key('Tab').defaultPrevented);
  assert.equal(doc.activeElement, first);
  assert(key('Escape').defaultPrevented);
  assert.equal(doc.activeElement, toggle);
  assert(sidebar.hasAttribute('inert'));
  assert(!main.hasAttribute('inert'));
});

test('clicking the overlay closes the drawer and restores focus', t => {
  const { doc, toggle, sidebar } = setup(t);
  toggle.click();
  doc.getElementById('sidebar-overlay').click();
  assert.equal(doc.activeElement, toggle);
  assert(sidebar.hasAttribute('inert'));
  assert.equal(doc.getElementById('sidebar-overlay').getAttribute('aria-hidden'), 'true');
});

test('background HTMX polling preserves the open menu and keyboard focus', t => {
  const { doc, toggle, main, settle } = setup(t);
  toggle.click();
  const focused = doc.activeElement;
  settle(doc.getElementById('poll'));
  assert.equal(doc.activeElement, focused);
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert(main.hasAttribute('inert'));
});

test('HTMX navigation and back/forward restoration focus the page and update the current link', t => {
  const ui = setup(t);
  ui.toggle.click();
  ui.navigate('/queue');
  assert.equal(ui.doc.activeElement, ui.main);
  assert(!ui.main.hasAttribute('inert'));
  assert(ui.sidebar.hasAttribute('inert'));
  assert.equal(ui.toggle.getAttribute('aria-expanded'), 'false');
  assert.equal(ui.sidebar.querySelector('[aria-current="page"]').getAttribute('href'), '/queue');
  ui.navigate('/', true);
  assert.equal(ui.sidebar.querySelector('[aria-current="page"]').getAttribute('href'), '/');
  assert.equal(ui.doc.activeElement, ui.main);
  ui.toggle.click();
  assert.equal(ui.toggle.getAttribute('aria-expanded'), 'true');
  assert.equal(ui.doc.activeElement, ui.sidebar.querySelector('a'));
});

test('desktop navigation stays available and responsive changes never strand focus', t => {
  const { doc, sidebar, main, toggle, resize, key } = setup(t, true);
  assert(!sidebar.hasAttribute('inert'));
  assert.equal(sidebar.getAttribute('aria-hidden'), 'false');
  sidebar.querySelector('a').focus();
  assert(!key('Tab').defaultPrevented);
  resize(false);
  assert.equal(doc.activeElement, toggle);
  assert(sidebar.hasAttribute('inert'));
  toggle.click();
  resize(true);
  assert(!sidebar.hasAttribute('inert'));
  assert(!main.hasAttribute('inert'));
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  toggle.focus();
  resize(true);
  assert.equal(doc.activeElement, sidebar.querySelector('[aria-current="page"]'));
});
