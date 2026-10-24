import original from './static.config.mjs';
import { fileURLToPath } from 'node:url';

const { launchOptions, ...use } = original.use;
export default {
  ...original,
  outputDir: process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/crossbrowser/static', import.meta.url)),
  use,
  projects: [
    { name: 'firefox-static', use: { browserName: 'firefox' } },
    { name: 'webkit-static', use: { browserName: 'webkit' } },
    { name: 'webkit-touch-static', use: { browserName: 'webkit', viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
};
