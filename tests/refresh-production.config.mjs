import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';
import {join} from 'node:path';
import {modes, originFor} from './refresh-production-fixture.mjs';

const root = fileURLToPath(new URL('..', import.meta.url)), mode = process.env.NEXTGO_REFRESH_TRANSPORT || 'http';
if (!modes.includes(mode)) throw new Error('Unknown NEXTGO_REFRESH_TRANSPORT');
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'refresh-production.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || join(root, `.nextgo-test-results/refresh/production-${mode}`),
  workers: 1, fullyParallel: false, timeout: 45_000, expect: {timeout: 10_000},
  use: {headless: true, trace: 'retain-on-failure', baseURL: originFor(mode)},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({name: `refresh-${mode}-${name}`, use: {
    browserName: name === 'webkit-touch' ? 'webkit' : name,
    ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
    ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {}),
  }})),
  webServer: {command: 'node tests/serve-refresh-production.mjs', url: originFor(mode) + '/refresh', cwd: root, timeout: 15_000, reuseExistingServer: false, gracefulShutdown: {signal: 'SIGTERM', timeout: 10_000}},
});
