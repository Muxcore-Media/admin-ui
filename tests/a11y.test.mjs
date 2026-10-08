import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import axe from 'axe-core';
import { JSDOM } from 'jsdom';

const root = fileURLToPath(new URL('../', import.meta.url));
// jsdom cannot compute painted colours or contrast. Keep all other axe rules,
// including region/landmark checks. This suite is not WCAG certification.
const options = { rules: { 'color-contrast': { enabled: false } } };

async function scan(markup) {
  const dom = new JSDOM(markup, { runScripts: 'outside-only', url: 'https://admin.example/' });
  try {
    dom.window.eval(axe.source);
    return await dom.window.axe.run(dom.window.document, options);
  } finally {
    dom.window.close();
  }
}

test('axe checks actual Go-rendered core journeys', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'admin-ui-a11y-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const result = spawnSync(process.env.GO || 'go', ['test', '-count=1', '-run', '^TestAccessibilityCoreJourneys$', './templ'], {
    cwd: root,
    env: { ...process.env, ADMIN_UI_A11Y_FIXTURE_DIR: dir },
    encoding: 'utf8',
    timeout: 180_000,
  });
  assert.equal(result.status, 0, `Go template fixtures failed: ${result.error || ''}\n${result.stdout}\n${result.stderr}`);
  const fixtures = readdirSync(dir).filter(name => name.endsWith('.html')).sort();
  assert.equal(fixtures.length, 35, 'render every core journey, including errors and repeated module setting keys');
  for (const fixture of fixtures) {
    await t.test(fixture, async () => {
      const results = await scan(readFileSync(join(dir, fixture), 'utf8'));
      assert.equal(results.violations.length, 0, JSON.stringify(results.violations.map(v => ({
        rule: v.id,
        impact: v.impact,
        elements: v.nodes.map(n => ({ target: n.target, message: n.failureSummary })),
      })), null, 2));
    });
  }

  // Keyboard behaviour of the content-rating page (ADR-0025): operable with
  // native controls only, in a sensible order, every control named.
  await t.test('content-ratings is keyboard operable in document order', () => {
    const dom = new JSDOM(readFileSync(join(dir, 'content-ratings.html'), 'utf8'));
    try {
      const doc = dom.window.document;
      const form = doc.querySelector('[data-testid="content-rating-form"]');
      assert(form, 'rating form present');
      const focusable = [...form.querySelectorAll('input:not([type=hidden]), select, button, a[href]')];
      assert(focusable.length > 0);
      for (const el of focusable) {
        assert.notEqual(el.getAttribute('tabindex'), '-1', `${el.outerHTML} must not be removed from tab order`);
        const name = el.getAttribute('aria-label')
          || (el.id && doc.querySelector(`label[for="${el.id}"]`)?.textContent.trim())
          || el.closest('label')?.textContent.trim()
          || (el.tagName === 'BUTTON' ? el.textContent.trim() : '');
        assert(name, `control has no accessible name: ${el.outerHTML}`);
      }
      // The first submit button in order is the bulk bar's, so implicit form
      // submission can never apply a single row's rating by accident.
      const submits = [...form.querySelectorAll('button[type=submit]')];
      assert.equal(submits[0].textContent.trim(), 'Apply to selected');
      assert(!submits[0].hasAttribute('name'), 'bulk button must not carry a row id');
      for (const row of submits.slice(1)) assert.equal(row.getAttribute('name'), 'only');
      // Bulk bar comes before the table; each row is checkbox, select, Set.
      const order = focusable.map(el => el.tagName.toLowerCase() + (el.type ? ':' + el.type : ''));
      assert.deepEqual(order.slice(0, 2), ['select:select-one', 'button:submit']);
      assert.deepEqual(order.slice(2, 5), ['input:checkbox', 'select:select-one', 'button:submit']);
      const total = doc.querySelectorAll('[data-testid="content-rating-row"]').length;
      assert.equal(doc.querySelectorAll('input[name=ids]').length, total, 'one checkbox per row');
    } finally {
      dom.window.close();
    }
  });
  await t.test('content-ratings result announces partial failure', () => {
    const dom = new JSDOM(readFileSync(join(dir, 'content-ratings-result-partial.html'), 'utf8'));
    try {
      const doc = dom.window.document;
      assert.equal(doc.querySelector('[data-testid="content-rating-summary"]').getAttribute('role'), 'status');
      assert.deepEqual([...doc.querySelectorAll('[data-testid="content-rating-outcome"]')].map(e => e.dataset.outcome), ['ok', 'failed', 'not-attempted']);
      assert(doc.querySelector('[data-testid="content-rating-summary"]').textContent.includes('Some titles were not changed'));
    } finally {
      dom.window.close();
    }
  });
});

test('axe control detects unlabeled controls and images', async () => {
  const result = await scan(`<!doctype html><html lang="en"><head><title>Control</title></head>
    <body><main><h1>Control</h1><button></button><img src="poster.jpg"></main></body></html>`);
  const rules = result.violations.map(v => v.id);
  assert(rules.includes('button-name'), `button-name missing: ${rules}`);
  assert(rules.includes('image-alt'), `image-alt missing: ${rules}`);
});
