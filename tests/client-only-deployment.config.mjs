import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';
import {deploymentModes} from './client-only-deployment-fixture.mjs';

export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'client-only.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/client-only-deployment/browser', import.meta.url)),
  workers: 1, fullyParallel: false, retries: 0, maxFailures: 1,
  timeout: 90_000, expect: {timeout: 15_000}, globalTimeout: 1_800_000,
  use: {headless: true, trace: 'retain-on-failure'},
  projects: deploymentModes.flatMap(mode => ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({
    name: `client-only-${mode}-${name}`,
    // Static exports freeze query-dependent props at export time. Failure-mode
    // query variants are exercised through all three real server transports.
    ...(mode === 'static' ? {grepInvert: /after eager and lazy setup/} : {}),
    use: {deploymentMode: mode, browserName: name === 'webkit-touch' ? 'webkit' : name,
      ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
      ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {})},
  }))),
});
