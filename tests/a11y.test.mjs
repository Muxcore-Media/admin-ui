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
  assert.equal(fixtures.length, 20, 'render every core journey, including errors and repeated module setting keys');
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
});

test('axe control detects unlabeled controls and images', async () => {
  const result = await scan(`<!doctype html><html lang="en"><head><title>Control</title></head>
    <body><main><h1>Control</h1><button></button><img src="poster.jpg"></main></body></html>`);
  const rules = result.violations.map(v => v.id);
  assert(rules.includes('button-name'), `button-name missing: ${rules}`);
  assert(rules.includes('image-alt'), `image-alt missing: ${rules}`);
});
