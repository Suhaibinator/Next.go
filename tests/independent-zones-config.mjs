import {defineConfig} from '@playwright/test';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('..',import.meta.url));
process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(),`nextgo-independent-zones-${process.pid}`);
process.env.NEXTGO_INDEPENDENT_ZONES_ROOT ||= join(process.env.NEXTGO_E2E_ROOT,'independent-zones');
export default defineConfig({
 testDir:fileURLToPath(new URL('.',import.meta.url)),testMatch:'independent-zones-spec.mjs',
 outputDir:process.env.NEXTGO_E2E_RESULTS||join(root,'.nextgo-test-results/independent-zones/browser'),
 workers:1,fullyParallel:false,timeout:60_000,expect:{timeout:20_000},
 use:{headless:true,trace:'retain-on-failure',baseURL:'http://127.0.0.1:4260'},
 projects:['chromium','firefox','webkit','webkit-touch'].map(name=>({name:`independent-zones-${name}`,use:{browserName:name==='webkit-touch'?'webkit':name,...(name==='webkit-touch'?{viewport:{width:390,height:844},isMobile:true,hasTouch:true}:{}),...(name==='chromium'&&process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}}:{})}})),
 webServer:{command:'node tests/independent-zones-serve.mjs',url:'http://127.0.0.1:4260/api/state',cwd:root,timeout:300_000,reuseExistingServer:false,gracefulShutdown:{signal:'SIGTERM',timeout:10_000}},
});
