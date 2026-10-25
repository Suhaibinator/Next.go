import {defineConfig} from '@playwright/test';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('..',import.meta.url));
process.env.NEXTGO_E2E_ROOT ||= join(tmpdir(),`nextgo-zones-dev-${process.pid}`);
process.env.NEXTGO_ZONES_DEV_ROOT ||= join(process.env.NEXTGO_E2E_ROOT,'zones-development');
export default defineConfig({
 testDir:fileURLToPath(new URL('.',import.meta.url)),testMatch:'zones-dev.spec.mjs',
 outputDir:process.env.NEXTGO_E2E_RESULTS||join(root,'.nextgo-test-results/zones/development-browser'),
 workers:1,fullyParallel:false,timeout:240_000,expect:{timeout:45_000},
 use:{headless:true,trace:'retain-on-failure',baseURL:'http://127.0.0.1:4236'},
 projects:['chromium','firefox','webkit','webkit-touch'].map(name=>({name:`zones-dev-${name}`,use:{browserName:name==='webkit-touch'?'webkit':name,...(name==='webkit-touch'?{viewport:{width:390,height:844},isMobile:true,hasTouch:true}:{}),...(name==='chromium'&&process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}}:{})}})),
 webServer:{command:'node tests/serve-zones-dev.mjs',url:'http://127.0.0.1:4236/_nextgo/inspect',cwd:root,timeout:300_000,reuseExistingServer:false,gracefulShutdown:{signal:'SIGTERM',timeout:15_000}},
});
