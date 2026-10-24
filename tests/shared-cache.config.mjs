import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { firstURL } from './shared-cache-fixture.mjs';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-shared-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)),
  testMatch: 'shared-cache.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || '.nextgo-test-results/shared-cache/browser',
  workers: 1, fullyParallel: false, timeout: 60_000, expect: { timeout: 15_000 },
  use: { headless: true, trace: 'retain-on-failure', baseURL: firstURL },
  projects: [
    { name: 'shared-chromium', use: { browserName: 'chromium', ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } },
    { name: 'shared-firefox', use: { browserName: 'firefox' } },
    { name: 'shared-webkit', use: { browserName: 'webkit' } },
    { name: 'shared-touch', use: { browserName: 'webkit', viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
  webServer: { command: 'node tests/serve-shared-cache.mjs', url: `${firstURL}/shared-cache`, cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 180_000, reuseExistingServer: false },
});
