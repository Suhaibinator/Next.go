import {chromium} from '../node_modules/playwright-core/index.mjs';
import {gzipSync} from 'node:zlib';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import os from 'node:os';
const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH});
const samples=[];
try {
 for(let i=0;i<10;i++){
  const context=await browser.newContext();const page=await context.newPage();
  await page.addInitScript(()=>{
   window.lazyMemories=[];
   const native=WebAssembly.instantiate;
   WebAssembly.instantiate=async(...args)=>{const result=await native(...args);window.lazyMemories.push((result.instance||result).exports.mem);return result};
  });
  const assets=[];page.on('response',r=>{if(r.url().endsWith('.wasm'))assets.push(r)});
  const started=performance.now();await page.goto(new URL(process.env.NEXTGO_BENCH_PATH||'/lazy',process.env.NEXTGO_BENCH_ORIGIN||'http://127.0.0.1:3000').href);await page.locator('body[data-nextgo-hydration="ready"]').waitFor();
  const elapsed=performance.now()-started;
  const memory=await page.evaluate(()=>window.lazyMemories.map(m=>m.buffer.byteLength));
  const bytes=[];for(const r of assets){
   let b,bodySource='browser-response';
   try{b=await r.body()}catch{
    // Debugger response bodies can be unavailable by hydration readiness.
    // Read the same immutable generation URL outside the measured window.
    const artifact=await context.request.get(r.url(),{failOnStatusCode:true});
    b=await artifact.body();bodySource='artifact-get-after-ready';await artifact.dispose();
   }
   bytes.push({file:new URL(r.url()).pathname.split('/').pop(),sha256:createHash('sha256').update(b).digest('hex'),raw:b.length,gzip9:gzipSync(b,{level:9}).length,bodySource})
  }
  samples.push({elapsedMS:Math.round(elapsed),linearMemoryBytes:memory,assets:bytes});await context.close();
 }
 console.log(JSON.stringify({date:new Date().toISOString(),path:process.env.NEXTGO_BENCH_PATH||'/lazy',environment:{go:execFileSync('go',['version'],{encoding:'utf8'}).trim(),browser:browser.version(),node:process.version,platform:os.platform(),arch:os.arch(),release:os.release(),cpu:os.cpus()[0]?.model},scope:'10 new Chromium contexts, one browser process, warm local Go dev server, no CPU/network throttling; not a production timing guarantee',samples},null,2));
}finally{await browser.close()}
