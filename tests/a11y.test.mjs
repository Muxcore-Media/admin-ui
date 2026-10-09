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
  assert.equal(fixtures.length, 43, 'render every core journey, including request mutation errors, per-item and bulk content rating states and repeated module setting keys');
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
  await t.test('content rating controls select page contents with bundled HTMX', async () => {
    const markup = readFileSync(join(dir, 'content-rating-movie.html'), 'utf8');
    const htmxSource = readFileSync(join(root, 'assets/htmx.min.js'), 'utf8');
    for (const target of ['form', 'a[href$="/item/fixture"]', 'a[href$="/content-rating"]']) {
      for (const removeSelector of [false, true]) {
        const dom = new JSDOM(markup, { runScripts: 'outside-only', url: 'https://admin.example/' });
        try {
          const { window } = dom;
          // jsdom requires an explicit XPath result type; browsers default it.
          const evaluate = window.XPathExpression.prototype.evaluate;
          window.XPathExpression.prototype.evaluate = function(context, type, result) {
            return evaluate.call(this, context, type ?? 0, result);
          };
          window.eval(htmxSource);
          const control = window.document.querySelector(target);
          assert(control, `missing ${target}`);
          if (removeSelector) control.removeAttribute('hx-select');
          // Exercise this repository's bundled HTMX attribute inheritance and
          // response selection, including Layout's hx-disinherit boundary.
          const select = window.htmx._('re')(control, 'hx-select');
          window.htmx.swap(window.document.querySelector('#main-content'), markup,
            { swapStyle: 'innerHTML', settleDelay: 0 }, { select, contextElement: control });
          const expected = removeSelector ? 2 : 1;
          assert.equal(window.document.querySelectorAll('#main-content').length, expected);
          assert.equal(window.document.querySelectorAll('#sidebar').length, expected);
          if (!removeSelector) {
            const choice = window.document.querySelector('select[name="classification"]');
            choice.focus();
            assert.equal(window.document.activeElement, choice);
            choice.value = 'unrated';
            const form = choice.closest('form');
            assert.equal(new window.FormData(form).get('classification'), 'unrated');
            const save = form.querySelector('button[type="submit"]');
            save.focus();
            assert.equal(window.document.activeElement, save);
          }
          await new Promise(resolve => window.setTimeout(resolve, 0));
        } finally {
          dom.window.close();
        }
      }
    }
  });
  await t.test('all five request mutation forms display errors with bundled HTMX', async () => {
    const htmxSource = readFileSync(join(root, 'assets/htmx.min.js'), 'utf8');
    const csrfSource = readFileSync(join(root, 'assets/csrf.js'), 'utf8');
    let formsChecked = 0;
    for (const page of ['request', 'approvals']) {
      const markup = readFileSync(join(dir, `${page}.html`), 'utf8');
      const response = readFileSync(join(dir, `${page}-action-error.html`), 'utf8');
      const selectors = page === 'request'
        ? ['form[action="/request"]', 'form[action$="/approve"]', 'form[action$="/deny"]']
        : ['form[action$="/approve"]', 'form[action$="/deny"]'];
      for (const target of selectors) {
        formsChecked++;
        for (const removeSelector of [false, true]) {
          const dom = new JSDOM(markup, { runScripts: 'outside-only', url: 'https://admin.example/' });
          try {
            const { window } = dom;
            const evaluate = window.XPathExpression.prototype.evaluate;
            window.XPathExpression.prototype.evaluate = function(context, type, result) {
              return evaluate.call(this, context, type ?? 0, result);
            };
            window.eval(htmxSource);
            window.eval(csrfSource);
            const control = window.document.querySelector(`form[method="post"]${target.slice(4)}`);
            assert(control, `missing ${page} ${target}`);
            if (removeSelector) control.removeAttribute('hx-select');
            for (const optIn of [null, '1']) {
              const detail = {
                shouldSwap: false, isError: true,
                xhr: { status: 403, getResponseHeader: name => name === 'X-Admin-Swap-Error' ? optIn : null },
              };
              window.document.dispatchEvent(new window.CustomEvent('htmx:beforeSwap', { detail }));
              assert.equal(detail.shouldSwap, optIn === '1');
            }
            const select = window.htmx._('re')(control, 'hx-select');
            window.htmx.swap(window.document.querySelector('#main-content'), response,
              { swapStyle: 'innerHTML', settleDelay: 0 }, { select, contextElement: control });
            // The deliberately removed selector detects whole-document nesting.
            assert.equal(window.document.querySelectorAll('#main-content').length, removeSelector ? 2 : 1);
            assert.equal(window.document.querySelectorAll('#sidebar').length, removeSelector ? 2 : 1);
            assert(window.document.querySelector('#main-content [role="alert"]'));
            assert.equal(window.document.querySelectorAll('#main-content form').length, 0);
            if (!removeSelector) {
              const reload = window.document.querySelector('#main-content a');
              assert.equal(reload.getAttribute('href'), `/${page}`);
              assert.equal(window.htmx._('re')(reload, 'hx-select'), '#main-content > *');
              reload.focus();
              assert.equal(window.document.activeElement, reload);
            }
            await new Promise(resolve => window.setTimeout(resolve, 0));
          } finally {
            dom.window.close();
          }
        }
      }
    }
    assert.equal(formsChecked, 5);
  });

  // Keyboard behaviour of the bulk content-rating page (ADR-0025): operable with
  // native controls only, in a sensible order, every control named.
  await t.test('bulk content-ratings is keyboard operable in document order', () => {
    const dom = new JSDOM(readFileSync(join(dir, 'content-ratings-bulk.html'), 'utf8'));
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
  await t.test('bulk content-ratings result announces partial failure', () => {
    const dom = new JSDOM(readFileSync(join(dir, 'content-ratings-bulk-result-partial.html'), 'utf8'));
    try {
      const doc = dom.window.document;
      assert.equal(doc.querySelector('[data-testid="content-rating-summary"]').getAttribute('role'), 'status');
      assert.deepEqual([...doc.querySelectorAll('[data-testid="content-rating-outcome"]')].map(e => e.dataset.outcome), ['ok', 'failed', 'uncertain', 'not-attempted']);
      assert(doc.querySelector('[data-testid="content-rating-summary"]').textContent.includes('Not every title was confirmed as changed'));
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
