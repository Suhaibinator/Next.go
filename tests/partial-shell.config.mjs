import original from './partial.config.mjs';
import { fileURLToPath } from 'node:url';

export default {
  ...original, testMatch: 'partial-shell.spec.mjs', timeout: 45_000,
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/partial-shell/browser', import.meta.url)),
  webServer: { ...original.webServer, env: { ...process.env, NEXTGO_PARTIAL_SHELL_TEST: 'true' } },
};
