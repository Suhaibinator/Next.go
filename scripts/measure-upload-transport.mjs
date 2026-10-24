// Compare actual local HTTP request-body sizes for the demo's two upload modes.
// The proxy counts streamed bytes; it never logs or retains selected contents.
import http from 'node:http';
import os from 'node:os';
import {execFileSync} from 'node:child_process';
import {chromium} from '../node_modules/playwright-core/index.mjs';
const target=new URL(process.env.NEXTGO_BENCH_ORIGIN||'http://127.0.0.1:3010');
if(target.protocol!=='http:'||!['127.0.0.1','localhost'].includes(target.hostname))throw new Error('This observation requires a local HTTP demo server');
const samples=[],sockets=new Set();
const proxy=http.createServer((request,response)=>{
 const url=new URL(request.url,target);if(url.origin!==target.origin){response.writeHead(400).end();return}
 let bytes=0;
 // The dev server binds its trusted Host to its stable address. Translate only
 // this local observation origin; do not relax the framework's Host/Origin rules.
 const localOrigin='http://'+request.headers.host;
 if(request.headers.origin&&request.headers.origin!==localOrigin){response.writeHead(403).end();return}
 const headers={...request.headers,host:target.host};if(headers.origin)headers.origin=target.origin;
 const outgoing=http.request(url,{method:request.method,headers},incoming=>{response.writeHead(incoming.statusCode,incoming.headers);incoming.pipe(response)});
 outgoing.on('error',()=>{if(!response.headersSent)response.writeHead(502);response.end()});
 request.on('data',chunk=>bytes+=chunk.length);
 request.on('end',()=>{if(request.method==='POST'&&url.pathname.endsWith('/files/receive/v1'))samples.push({contentType:request.headers['content-type'],bodyBytes:bytes,declaredContentLength:request.headers['content-length']?Number(request.headers['content-length']):null})});
 request.on('aborted',()=>outgoing.destroy());response.on('close',()=>outgoing.destroy());request.pipe(outgoing);
});
proxy.on('connection',socket=>{sockets.add(socket);socket.on('close',()=>sockets.delete(socket))});
await new Promise((resolve,reject)=>{proxy.once('error',reject);proxy.listen(0,'127.0.0.1',resolve)});
let browser;
try{
 browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH});
 const origin='http://127.0.0.1:'+proxy.address().port;
 for(const mode of ['multipart','json']){
  const context=await browser.newContext(),page=await context.newPage();
  if(process.env.NEXTGO_BENCH_DEBUG){page.on('console',m=>console.error('browser',m.type(),m.text()));page.on('pageerror',e=>console.error('page',e.message));page.on('requestfailed',r=>console.error('request',r.url(),r.failure()));page.on('response',r=>{if(r.status()>=400)console.error('response',r.status(),r.url())})}
  const document=await page.goto(origin+'/uploads?transport='+mode);if(document.status()!==200)throw new Error('Proxy document status '+document.status());await page.locator('body[data-nextgo-hydration="ready"]').waitFor();
  await page.getByLabel('Upload label',{exact:true}).fill('Transport body observation');
  await page.getByLabel('Files',{exact:true}).setInputFiles([{name:'first.bin',mimeType:'application/octet-stream',buffer:Buffer.alloc(256*1024,0)},{name:'second.bin',mimeType:'application/octet-stream',buffer:Buffer.alloc(256*1024,255)}]);
  await page.getByRole('button',{name:'Receive files',exact:true}).click();await page.locator('[data-file-status]').filter({hasText:'Files received.'}).waitFor();
  if(samples.length!==(mode==='multipart'?1:2))throw new Error('Unexpected request count');samples.at(-1).mode=mode;
  const sample=samples.at(-1);if(sample.declaredContentLength!==null&&sample.declaredContentLength!==sample.bodyBytes)throw new Error('Request count differs from declared length');
  await context.close();
 }
 console.log(JSON.stringify({date:new Date().toISOString(),sourceOrigin:target.origin,environment:{go:execFileSync('go',['version'],{encoding:'utf8'}).trim(),node:process.version,browser:browser.version(),platform:os.platform(),arch:os.arch(),cpu:os.cpus()[0]?.model},scope:'One synthetic two-file request per mode through a local byte-counting proxy; 524288 content bytes, no request compression. This is not latency, throughput, allocation, cold-start, cloud or general performance qualification.',samples},null,2));
}finally{await browser?.close();for(const socket of sockets)socket.destroy();await new Promise(resolve=>proxy.close(resolve));http.globalAgent.destroy()}
