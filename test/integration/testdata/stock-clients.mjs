import assert from 'node:assert/strict';
import { chromium } from 'playwright-core';
import puppeteer from 'puppeteer-core';

const [endpoint, url, which] = process.argv.slice(2);

async function viaPuppeteer() {
  const browser = await puppeteer.connect({ browserURL: endpoint, defaultViewport: null });
  try {
    const page = await browser.newPage();
    await page.bringToFront();
    await page.goto(url);
    assert.equal(await page.title(), 'cdp endpoint fixture');
    await page.type('#name', 'puppeteer');
    await page.click('#go');
    assert.equal(await page.$eval('#out', (el) => el.textContent), 'hello puppeteer');
    const shot = await page.screenshot();
    assert.ok(shot.length > 100, 'an empty screenshot');
    await page.close();
  } finally {
    await browser.disconnect();
  }
}

async function viaPlaywright() {
  const browser = await chromium.connectOverCDP(endpoint);
  const context = browser.contexts()[0];
  const page = await context.newPage();
  await page.bringToFront();
  await page.goto(url);
  assert.equal(await page.title(), 'cdp endpoint fixture');
  await page.fill('#name', 'playwright');
  await page.click('#go');
  assert.equal(await page.textContent('#out'), 'hello playwright');
  assert.equal(await page.evaluate(() => 6 * 7), 42);
  const shot = await page.screenshot();
  assert.ok(shot.length > 100, 'an empty screenshot');
  await page.close();
}

await (which === 'puppeteer' ? viaPuppeteer() : viaPlaywright());
console.log(`${which} ok`);
process.exit(0);
