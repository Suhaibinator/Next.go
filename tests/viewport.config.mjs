import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-viewport-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'viewport.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/viewport/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 45_000, expect: { timeout: 12_000 },
  use: { headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4171' },
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({ name: `viewport-${name}`, use: { browserName: name === 'webkit-touch' ? 'webkit' : name, ...(name === 'webkit-touch' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } })),
  webServer: { command: 'node tests/serve-viewport.mjs', url: 'http://127.0.0.1:4171/_fixture/ready', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 240_000, reuseExistingServer: false },
});
