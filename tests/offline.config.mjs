import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'offline.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/offline/browser', import.meta.url)),
  workers: 1, fullyParallel: false, retries: 0, maxFailures: 1,
  timeout: 90_000, expect: {timeout: 20_000},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({name: `offline-${name}`, metadata: {offlineBrowser: name}})),
});
