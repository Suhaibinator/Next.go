import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';

const profiles = [
  {name:'chromium', use:{browserName:'chromium', ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? {launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}} : {})}},
  {name:'firefox', use:{browserName:'firefox'}},
  {name:'webkit', use:{browserName:'webkit'}},
  {name:'webkit-touch', use:{browserName:'webkit', viewport:{width:390,height:844}, isMobile:true, hasTouch:true}},
];
const variants = ['EAGER_ROOT','EAGER_MOUNTED','LAZY_ROOT','LAZY_MOUNTED'];

// No webServer, global setup, build hook, native process, or inherited config.
// Artifacts are supplied explicitly through per-variant environment pins.
export default defineConfig({
  testDir:fileURLToPath(new URL('.', import.meta.url)),
  testMatch:'static-shell.spec.mjs',
  outputDir:process.env.NEXTGO_E2E_RESULTS || fileURLToPath(new URL('../.nextgo-test-results/static-shell-browser', import.meta.url)),
  fullyParallel:false,
  workers:1,
  retries:0,
  maxFailures:1,
  timeout:60000,
  globalTimeout:15 * 60000,
  expect:{timeout:15000},
  use:{headless:true, trace:'retain-on-failure', screenshot:'only-on-failure'},
  projects:variants.flatMap(variant => profiles.map(profile => ({name:variant.toLowerCase() + '-' + profile.name, metadata:{staticShellVariant:variant}, use:profile.use}))),
});
