#!/usr/bin/env node

import { access, stat } from 'node:fs/promises';

const [input, scale, output] = process.argv.slice(2);
const browsersPath = '/opt/newcover-worker/browsers';

async function main() {
  if (!input || !['2', '4'].includes(scale) || !output) {
    throw new Error(
      'Usage: upscale.mjs <input-image> <2|4> <output-image>'
    );
  }

  await access(input);

  process.env.PLAYWRIGHT_BROWSERS_PATH = browsersPath;

  const { chromium } = await import('playwright');
  const browser = await chromium.launch({ headless: true });

  try {
    const page = await browser.newPage({ acceptDownloads: true });

    await page.goto('https://imgupscaler.com/', {
      waitUntil: 'domcontentloaded',
      timeout: 60000
    });

    await page.locator('input[type=file]').first().setInputFiles(input);

    await page.locator(
      `input[name="ratio_batch"][value="${scale}"]`
    ).check();

    console.log(`Selected ${scale}×`);

    await page.getByRole('button', {
      name: 'Upload and start'
    }).click();

    console.log('Uploaded. Waiting for processing...');

    const downloadButton = page.getByRole('button', {
      name: 'Download',
      exact: true
    });

    await downloadButton.waitFor({
      state: 'visible',
      timeout: 300000
    });

    console.log('Processing finished. Downloading...');

    const downloadPromise = page.waitForEvent('download', {
      timeout: 60000
    });

    await downloadButton.click();

    const download = await downloadPromise;
    await download.saveAs(output);

    const result = await stat(output);

    if (!result.isFile() || result.size === 0) {
      throw new Error('Downloaded output is missing or empty');
    }

    console.log(`Downloaded: ${output}`);
    console.log(`Size: ${result.size} bytes`);
  } finally {
    await browser.close();
  }
}

main().catch(error => {
  console.error(`[ERROR] ImgUpscaler: ${error.message}`);
  process.exitCode = 1;
});
