import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-action-view-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'action-view.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/action-view/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 30_000, expect: { timeout: 10_000 },
  use: { headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4185' },
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({ name: `action-view-${name}`, use: { browserName: name === 'webkit-touch' ? 'webkit' : name, ...(name === 'webkit-touch' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } })),
  webServer: { command: 'node tests/serve-action-view.mjs', url: 'http://127.0.0.1:4185/api/health', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 180_000, reuseExistingServer: false },
});
