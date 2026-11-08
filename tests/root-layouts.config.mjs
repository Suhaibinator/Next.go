import {defineConfig} from '@playwright/test';
import {fileURLToPath} from 'node:url';
export default defineConfig({
 testDir:fileURLToPath(new URL('.',import.meta.url)),testMatch:'root-layouts.spec.mjs',outputDir:process.env.NEXTGO_E2E_RESULTS||fileURLToPath(new URL('../.nextgo-test-results/root-layouts/browser',import.meta.url)),
 workers:1,fullyParallel:false,retries:0,maxFailures:1,timeout:90_000,expect:{timeout:15_000},globalTimeout:1_800_000,use:{headless:true,trace:'retain-on-failure'},
 projects:['http','buffered','streaming','static'].flatMap(mode=>['chromium','firefox','webkit','webkit-touch'].map(name=>({name:`root-layouts-${mode}-${name}`,
 ...(mode==='static'?{grepInvert:/conditional rewrite/}:{}),
 use:{rootTransport:mode,browserName:name==='webkit-touch'?'webkit':name,...(name==='webkit-touch'?{viewport:{width:390,height:844},isMobile:true,hasTouch:true}:{}),...(name==='chromium'&&process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}}:{})}}))),
});
