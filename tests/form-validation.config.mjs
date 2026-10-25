import {defineConfig} from '@playwright/test';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(), `nextgo-form-validation-${process.pid}`);
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'form-validation.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || join(root, '.nextgo-test-results/form-validation/browser'),
  workers: 1, fullyParallel: false, timeout: 45_000, expect: {timeout: 10_000},
  use: {headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4220'},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({
    name: `form-validation-${name}`,
    use: {
      browserName: name === 'webkit-touch' ? 'webkit' : name,
      ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
      ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {}),
    },
  })),
  webServer: {command: 'node tests/serve-form-validation.mjs', url: 'http://127.0.0.1:4220/forms-demo/api/health', cwd: root, timeout: 240_000, reuseExistingServer: false, gracefulShutdown: {signal: 'SIGTERM', timeout: 5000}},
});
