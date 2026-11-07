import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const sessionKey = 'yuanfa.guide.v1';
export const stored = page => page.evaluate(key => JSON.parse(sessionStorage.getItem(key)), sessionKey);
export async function launchBrowser() {
  const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || resolve('node_modules/playwright-core/index.mjs')).href);
  return chromium.launch({ executablePath: process.env.BROWSER_PATH, headless: true, args: ['--no-sandbox'], env: { ...process.env, TMPDIR: '/proc/self/cwd/.work/browser-tmp' } });
}
export async function shot(page, out, name, report) {
  await mkdir(out, { recursive: true });
  await page.screenshot({ path: join(out, name), fullPage: false });
  report.screenshots.push(join(out, name));
}
export async function readable(page) {
  const size = await page.evaluate(() => ({ viewport: innerWidth, content: document.documentElement.scrollWidth }));
  assert.ok(size.content <= size.viewport + 1, JSON.stringify(size));
  const dialog = page.locator('.yg-panel');
  if (await dialog.count() && await dialog.isVisible()) {
    const box = await dialog.boundingBox();
    assert.ok(box.x >= 0 && box.x + box.width <= size.viewport + 1, 'guide fits viewport');
  }
  return size;
}
export async function openGuide(page) {
  await page.locator('.yg-launcher').click();
  await page.getByRole('dialog', { name: '工具选型导购' }).waitFor();
  await page.waitForFunction(() => !document.querySelector('#guide-message').disabled);
}
export async function send(page, text) {
  await page.getByLabel('作业需求或条件更正').fill(text);
  await page.keyboard.press('Enter');
}
export async function result(page, state) {
  await page.locator('[data-guide-state="' + state + '"]').waitFor();
  await page.waitForFunction(key => JSON.parse(sessionStorage.getItem(key))?.pending_turn === null, sessionKey);
  return (await stored(page)).result;
}
export async function noActions(page) {
  assert.equal(await page.locator('[data-guide-selection], [data-guide-purchase], .yg-result button').count(), 0);
}
export async function failure(page) {
  await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor();
  await noActions(page);
}
export async function saveReport(out, report) {
  await mkdir(out, { recursive: true });
  await writeFile(join(out, 'result.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
}
