import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-stream-admission-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'stream-admission.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/stream-admission', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 30_000, expect: { timeout: 5000 },
  use: { headless: true, trace: 'retain-on-failure' },
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({ name: `stream-admission-${name}`, use: { browserName: name === 'webkit-touch' ? 'webkit' : name, ...(name === 'webkit-touch' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } })),
  webServer: { command: 'node tests/serve-stream-admission.mjs', url: 'http://127.0.0.1:4161/api/health', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 180_000, reuseExistingServer: false },
});
