import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
process.env.NEXTGO_STATIC_E2E_ROOT ||= join(tmpdir(), `nextgo-static-e2e-${process.pid}`);

export default defineConfig({
  testDir: '.',
  testMatch: 'static.spec.mjs',
  outputDir: join(root, '.nextgo-test-results', 'static'),
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: 'http://127.0.0.1:4102',
    headless: true,
    trace: 'retain-on-failure',
    ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } }
      : {}),
  },
  webServer: {
    command: 'node tests/serve-static.mjs',
    cwd: root,
    url: 'http://127.0.0.1:4102/',
    timeout: 180_000,
    reuseExistingServer: false,
  },
});
