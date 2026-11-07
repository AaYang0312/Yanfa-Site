import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { createServer } from 'node:http';
import { resolve } from 'node:path';
import { build } from 'esbuild';
import { chromium } from 'playwright-core';
import cases from './consumer-cases.json' with { type: 'json' };

const evidence = process.argv[2];
if (!evidence) throw new Error('Pass an evidence directory as the first argument.');
await mkdir(evidence, { recursive: true });
const bundle = await build({
  absWorkingDir: process.cwd(),
  stdin: { contents: `import {decodeContract,validateContract} from ${JSON.stringify(resolve('src/generated/validate-contract.mjs'))};window.contractAPI={decodeContract,validateContract};`, resolveDir: process.cwd() },
  platform: 'browser', bundle: true, format: 'iife', write: false,
});
const script = bundle.outputFiles[0].text;
await writeFile(resolve(evidence, 'browser-validator.bundle.js'), script);
const server = createServer((request, response) => {
  if (request.url === '/validator.js') {
    response.setHeader('Content-Type', 'text/javascript'); response.end(script);
  } else {
    response.setHeader('Content-Type', 'text/html');
    response.end('<!doctype html><meta charset="utf-8"><title>Shared contract consumers</title><h1>Shared contract consumer regressions</h1><pre id="results"></pre><script src="/validator.js"></script>');
  }
});
let browser;
try {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || chromium.executablePath(), args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1000, height: 900 } });
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  const rows = await page.evaluate(cases => cases.map(entry => {
    try {
      const result = window.contractAPI.decodeContract(entry.schema, entry.raw);
      return { name: entry.name, accepted: true, result, revisionString: result.expected_workspace_revision === undefined ? undefined : String(result.expected_workspace_revision), finiteRevision: result.expected_workspace_revision === undefined ? undefined : Number.isFinite(result.expected_workspace_revision), safeRevision: result.expected_workspace_revision === undefined ? undefined : Number.isSafeInteger(result.expected_workspace_revision) };
    } catch (error) {
      return { name: entry.name, accepted: false, status: error.status, error: error.error ?? String(error) };
    }
  }), cases);
  await page.locator('#results').evaluate((node, rows) => { node.textContent = rows.map(row => `${row.accepted ? 'ACCEPT' : `REJECT ${row.status}`} ${row.name}`).join('\n'); }, rows);
  await page.screenshot({ path: resolve(evidence, 'browser-consumers.png'), fullPage: true });
  await writeFile(resolve(evidence, 'browser-consumer-observations.json'), JSON.stringify({ browser: browser.version(), pageErrors: errors, observations: rows }, null, 2) + '\n');
  assert.deepEqual(errors, []);
  for (let index = 0; index < cases.length; index++) {
    const entry = cases[index]; const row = rows[index];
    assert.equal(row.accepted, entry.valid, entry.name);
    if (entry.valid) {
      assert.deepEqual(row.result, entry.expected, entry.name);
      if (Object.hasOwn(row.result, 'expected_workspace_revision')) { assert.equal(row.finiteRevision, true); assert.equal(row.safeRevision, true); }
    } else {
      assert.equal(row.status, 422, entry.name);
      for (const field of entry.fields) assert.ok(row.error.fields.some(path => path === field || path.startsWith(`${field}.`)), `${entry.name}: ${field}: ${row.error.fields}`);
    }
  }
  console.log(JSON.stringify({ browser: browser.version(), observations: rows.map(({ result, ...row }) => row) }, null, 2));
  await page.close();
} finally {
  if (browser) await browser.close();
  await new Promise(resolve => server.close(resolve));
}
