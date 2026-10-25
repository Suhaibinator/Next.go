import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-generated-actions-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'generated-actions.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/generated-actions/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 30_000, expect: { timeout: 10_000 },
  use: { headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4181' },
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({ name: `generated-actions-${name}`, use: { browserName: name === 'webkit-touch' ? 'webkit' : name, ...(name === 'webkit-touch' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } })),
  webServer: { command: 'node tests/serve-generated-actions.mjs', url: 'http://127.0.0.1:4181/api/health', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 180_000, reuseExistingServer: false },
});
