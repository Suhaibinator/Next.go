import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'social-image.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/social-image/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 45_000, expect: {timeout: 15_000},
  use: {headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4179/social-cards'},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({name: `social-image-${name}`, use: {browserName: name === 'webkit-touch' ? 'webkit' : name, ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}), ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {})}})),
  webServer: {command: 'node tests/serve-social-image.mjs', url: 'http://127.0.0.1:4179/social-cards/api/health', cwd: fileURLToPath(new URL('..', import.meta.url)), timeout: 240_000, reuseExistingServer: false},
});
