import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';

if (process.env.NEXTGO_METADATA_STATIC_ROOT) process.env.NEXTGO_METADATA_STATIC_URL ||= 'http://127.0.0.1:4207/metadata-site/';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)),
  testMatch: 'metadata-files.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/metadata-files/browser', import.meta.url)),
  workers: 1, fullyParallel: false, timeout: 45_000, expect: {timeout: 15_000},
  use: {headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4205'},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({
    name: `metadata-files-${name}`,
    use: {
      browserName: name === 'webkit-touch' ? 'webkit' : name,
      ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
      ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {}),
    },
  })),
  webServer: {
    command: 'node tests/serve-metadata-files.mjs',
    url: 'http://127.0.0.1:4205/metadata-site/api/health',
    cwd: fileURLToPath(new URL('..', import.meta.url)),
    timeout: 60_000, reuseExistingServer: false,
  },
});
