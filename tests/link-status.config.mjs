import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';
import {isAbsolute} from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
if (!isAbsolute(process.env.NEXTGO_E2E_SERVER_BINARY || '') || !/^[a-f0-9]{64}$/.test(process.env.NEXTGO_LINK_STATUS_SERVER_SHA256 || '') || !isAbsolute(process.env.NEXTGO_LINK_STATUS_PROCESS_PROOF || '')) throw new Error('LinkStatus qualification requires pinned prebuilt server and owned proof path; building during browser tests is forbidden');
export default defineConfig({
  testDir: fileURLToPath(new URL('.', import.meta.url)), testMatch: 'link-status.spec.mjs',
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/link-status/browser', import.meta.url)),
  workers: 1, fullyParallel: false, retries: 0, maxFailures: 1,
  timeout: 60_000, expect: {timeout: 15_000},
  use: {headless: true, trace: 'retain-on-failure', baseURL: 'http://127.0.0.1:4100'},
  projects: ['chromium', 'firefox', 'webkit', 'webkit-touch'].map(name => ({
    name: `link-status-${name}`,
    use: {
      browserName: name === 'webkit-touch' ? 'webkit' : name,
      ...(name === 'webkit-touch' ? {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true} : {}),
      ...(name === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions: {executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {}),
    },
  })),
  webServer: [{command: 'node tests/serve-link-status.mjs', url: 'http://127.0.0.1:4100/api/health', cwd: root, timeout: 30_000, reuseExistingServer: false, gracefulShutdown: {signal: 'SIGTERM', timeout: 5000}}],
});
