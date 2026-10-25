import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'lambda-features.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/lambda-features/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 60_000, expect: {timeout: 20_000},
  use: {headless: true, trace: 'retain-on-failure'},
  projects: ['buffered', 'streaming'].flatMap(mode => ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(engine => ({
    name: `lambda-${mode}-${engine}`, metadata: {mode}, use: {
      baseURL: `http://127.0.0.1:${mode === 'buffered' ? 4196 : 4197}`,
      browserName: engine === 'webkit-touch' ? 'webkit' : engine,
      ...(engine === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
      ...(engine === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {}),
    },
  }))),
  webServer: {command: 'node tests/serve-lambda-features.mjs', url: 'http://127.0.0.1:4197/_fixture/lambda/ready', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 30_000, reuseExistingServer: false, gracefulShutdown: {signal: 'SIGTERM', timeout: 10_000}},
});
