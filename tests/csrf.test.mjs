import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const script = readFileSync(new URL('../assets/csrf.js', import.meta.url), 'utf8');

function setup(t) {
  const dom = new JSDOM('<!doctype html><html lang="en"><body></body></html>', { url: 'https://admin.example/', runScripts: 'outside-only' });
  t.after(() => dom.window.close());
  dom.window.document.cookie = 'csrf-token=abc123';
  dom.window.eval(script);
  return dom.window;
}

function beforeSwap(window, status, header) {
  const detail = {
    shouldSwap: false,
    isError: true,
    xhr: { status, getResponseHeader: name => (name === 'X-Admin-Swap-Error' ? header : null) },
  };
  window.document.dispatchEvent(new window.CustomEvent('htmx:beforeSwap', { detail }));
  return detail;
}

test('adds the CSRF header to htmx requests', t => {
  const window = setup(t);
  const detail = { headers: {} };
  window.document.dispatchEvent(new window.CustomEvent('htmx:configRequest', { detail }));
  assert.equal(detail.headers['X-CSRF-Token'], 'abc123');
});

test('swaps a 4xx body only when the response opts in', t => {
  const window = setup(t);
  const optedIn = beforeSwap(window, 400, '1');
  assert.equal(optedIn.shouldSwap, true);
  assert.equal(optedIn.isError, false);
  for (const [status, header] of [[400, null], [403, null], [400, '0'], [500, '1']]) {
    const detail = beforeSwap(window, status, header);
    assert.equal(detail.shouldSwap, false, `status ${status} header ${header}`);
    assert.equal(detail.isError, true);
  }
});
