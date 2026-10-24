import { defineConfig } from '@playwright/test';
import original from '../playwright.config.mjs';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)),
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/stream-parser/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 30_000, expect: { timeout: 5000 },
  grep: /split parser|initial streaming shell stays intact|traced HTTP still flushes/,
  use: { headless: true, trace: 'retain-on-failure' },
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({ name: `stream-parser-${name}`, testMatch: ['stream-parser.spec.mjs', 'browser.spec.mjs', 'tracing.spec.mjs'], use: { browserName: name === 'webkit-touch' ? 'webkit' : name, baseURL: 'http://127.0.0.1:4100', ...(name === 'webkit-touch' ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } } : {}) } })),
  webServer: [original.webServer[0], original.webServer.find(server => server.command === 'node tests/serve-tracing.mjs'), { command: 'node tests/serve-stream-parser.mjs', url: 'http://127.0.0.1:4160/_fixture/ready', timeout: 30_000, reuseExistingServer: false }].map(server => ({ ...server, cwd: fileURLToPath(new URL('..', import.meta.url)) })),
});
