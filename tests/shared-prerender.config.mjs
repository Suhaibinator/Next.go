import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-shared-prerender-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'shared-prerender.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || '.nextgo-test-results/shared-prerender/browser',
  workers: 1, fullyParallel: false, timeout: 180_000, expect: { timeout: 15_000 },
  use: { headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4155' },
  projects: [
    { name: 'shared-prerender-chromium', use: { browserName: 'chromium', ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } },
    { name: 'shared-prerender-firefox', use: { browserName: 'firefox' } },
    { name: 'shared-prerender-webkit', use: { browserName: 'webkit' } },
    { name: 'shared-prerender-touch', use: { browserName: 'webkit', viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
  webServer: { command: 'node tests/serve-shared-prerender.mjs', url: 'http://127.0.0.1:4155/_fixture/ready', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 300_000, reuseExistingServer: false },
});
