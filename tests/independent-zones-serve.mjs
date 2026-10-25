import http from 'node:http';
import {randomBytes,timingSafeEqual} from 'node:crypto';
import {mkdir,writeFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {publicOrigin,ports,loadArtifacts,buildArtifacts,ownProcesses,waitPort,portOpen} from './independent-zones-fixture.mjs';

const root=process.cwd();
if(!process.env.NEXTGO_INDEPENDENT_ZONES_ROOT)throw new Error('Fixture workspace is required');
const owned=resolve(process.env.NEXTGO_INDEPENDENT_ZONES_ROOT);await mkdir(owned,{recursive:true});
const inherited=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('NEXTGO_ZONE_')&&!key.startsWith('NEXTGO_DEMO_')&&!key.startsWith('NEXTGO_LAMBDA_')&&!key.startsWith('AWS_LAMBDA_')&&!['NEXTGO_ENV_DIR','NEXTGO_ENV_MODE'].includes(key)));
const processes=ownProcesses({...inherited,GOTOOLCHAIN:'local',GOWORK:'off',GOMAXPROCS:'1',GOFLAGS:inherited.GOFLAGS||'-p=1'});
const secret=randomBytes(32).toString('hex'),transactions=new Set(),records=[];let sequence=0,snapshot='I1',relayMode='pass',dropNext=false,closing,stopping=false,artifacts;
let front,relay,parentWatch;
function record(value){const item={id:++sequence,...value};records.push(item);if(records.length>512)records.shift();return item;}
const hop=new Set(['connection','keep-alive','proxy-authenticate','proxy-authorization','te','trailer','transfer-encoding','upgrade','http2-settings']);
function headers(input){const skip=new Set([...hop,...String(input.connection||'').split(',').map(value=>value.trim().toLowerCase()).filter(Boolean)]);return Object.fromEntries(Object.entries(input).filter(([name])=>!skip.has(name.toLowerCase())));}
function legal(req,port){return req.headers.host===`127.0.0.1:${port}`&&typeof req.url==='string'&&req.url.startsWith('/')&&!req.url.startsWith('//')&&!/[\\\r\n#]/.test(req.url)&&req.url.length<=8192;}
function fail(res,status,message){if(res.headersSent){res.destroy();return;}res.writeHead(status,{'content-type':'text/plain; charset=utf-8','cache-control':'no-store'});res.end(message);}
function authenticated(req){const value=String(req.headers['x-fixture-key']||'');return value.length===secret.length&&timingSafeEqual(Buffer.from(value),Buffer.from(secret));}
async function control(req,res){
 if(!authenticated(req)){fail(res,403,'Fixture authorization required');return;}
 if(req.url!=='/_fixture-independent/control'){fail(res,404,'Unknown fixture control');return;}
 if(req.method==='POST'){
  if(!/^application\/json(?:\s*;\s*charset=utf-8)?$/i.test(String(req.headers['content-type']||''))){fail(res,415,'Expected fixture JSON');return;}
  const chunks=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>1024){fail(res,413,'Fixture control body exceeds bound');return;}chunks.push(chunk);}
  let input;try{input=JSON.parse(Buffer.concat(chunks).toString('utf8'));}catch{fail(res,400,'Invalid fixture JSON');return;}
  if(!input||Array.isArray(input)||Object.keys(input).some(key=>!['snapshot','relay','clear','dropNext'].includes(key))||input.snapshot!==undefined&&!['I1','I2','rollback'].includes(input.snapshot)||input.relay!==undefined&&!['pass','miswire'].includes(input.relay)||input.clear!==undefined&&typeof input.clear!=='boolean'||input.dropNext!==undefined&&typeof input.dropNext!=='boolean'){fail(res,400,'Invalid fixture control');return;}
  if(input.snapshot!==undefined)snapshot=input.snapshot;if(input.relay!==undefined)relayMode=input.relay;if(input.dropNext!==undefined)dropNext=input.dropNext;if(input.clear)records.length=0;
 }else if(req.method!=='GET'||Number(req.headers['content-length']||0)!==0||req.headers['transfer-encoding']){fail(res,405,'Fixture control method rejected');return;}
 res.writeHead(200,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify({snapshot,relay:relayMode,dropNext,records,hashes:artifacts.hashes}));
}

function forward(req,res,port,role,options={}){
 if(transactions.size>=32){fail(res,503,'Fixture exchange bound reached');return;}
 const pathname=new URL(req.url,publicOrigin).pathname,item=record({role,method:req.method,path:pathname,port,snapshot,...(role==='relay'?{target:options.target,idempotencyKeyPresent:Boolean(req.headers['idempotency-key'])}:{})});
 const ownedExchange={req,res,upstream:null};transactions.add(ownedExchange);let requestBytes=0,responseBytes=0,finished=false;
 const finish=()=>{if(finished)return;finished=true;clearTimeout(timer);transactions.delete(ownedExchange);};
 const upstream=http.request({hostname:'127.0.0.1',port,path:req.url,method:req.method,headers:headers(req.headers),agent:false},incoming=>{
  item.status=incoming.statusCode;
  const dropping=role==='relay'&&options.target==='A2'&&dropNext&&req.method==='POST'&&pathname==='/_nextgo/actions/preview/v1'&&incoming.statusCode===200;
  if(dropping){dropNext=false;item.dropAfterActualCommit=true;}
  else res.writeHead(incoming.statusCode,headers(incoming.headers));
  incoming.on('data',chunk=>{responseBytes+=chunk.length;if(responseBytes>32<<20){item.responseBoundExceeded=true;incoming.destroy();upstream.destroy();res.destroy();finish();return;}if(!dropping&&!res.write(chunk))incoming.pause();});
  res.on('drain',()=>incoming.resume());
  incoming.once('end',()=>{item.responseBytes=responseBytes;item.complete=true;if(dropping){res.destroy();}else res.end();finish();});
  incoming.once('aborted',()=>{item.aborted=true;res.destroy();finish();});incoming.once('error',()=>{item.error=true;res.destroy();finish();});
 });
 ownedExchange.upstream=upstream;
 const timer=setTimeout(()=>{item.timeout=true;upstream.destroy();res.destroy();finish();},25_000);
 upstream.once('error',()=>{item.error=true;fail(res,502,'Fixture upstream unavailable');finish();});
 req.on('data',chunk=>{requestBytes+=chunk.length;if(requestBytes>64<<10){item.requestBoundExceeded=true;upstream.destroy();fail(res,413,'Fixture request exceeds bound');finish();}});
 req.once('aborted',()=>{upstream.destroy();finish();});res.once('close',()=>{if(!res.writableEnded)upstream.destroy();finish();});
 req.pipe(upstream);
}

function server(handler){const value=http.createServer(handler);value.requestTimeout=30_000;value.headersTimeout=10_000;value.maxConnections=64;value.on('upgrade',(_req,socket)=>socket.destroy());return value;}
function listen(value,port){return new Promise((resolve,reject)=>{value.once('error',reject);value.listen(port,'127.0.0.1',resolve);});}
async function closeServer(value){if(!value)return;value.closeAllConnections();await new Promise(resolve=>value.close(resolve));}
function stop(code=0){if(closing)return closing;stopping=true;clearInterval(parentWatch);closing=(async()=>{
 for(const item of transactions){item.req.destroy();item.res.destroy();item.upstream?.destroy();}
 await Promise.all([closeServer(front),closeServer(relay)]);const children=await processes.close(),listeners=await Promise.all(Object.entries(ports).map(async([name,port])=>({name,port,closed:!await portOpen(port)})));
 const clean={at:new Date().toISOString(),children,listeners,cleanupPass:children.every(child=>child.closed)&&listeners.every(value=>value.closed)};await writeFile(join(owned,'cleanup.json'),JSON.stringify(clean,null,2)+'\n');
 if(!clean.cleanupPass){console.error('Independent zone cleanup failed');process.exitCode=1;}else process.exitCode=code;
 })();return closing;}
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{void stop();});
const parentPID=process.ppid;parentWatch=setInterval(()=>{let lost=process.ppid!==parentPID;try{process.kill(parentPID,0);}catch(error){if(error.code==='ESRCH')lost=true;}if(lost)void stop(1);},1000);

try{
 const index=process.env.NEXTGO_INDEPENDENT_ZONES_ARTIFACTS||await buildArtifacts(root,owned,processes,process.env.NEXTGO_E2E_NEXTGO_BINARY);artifacts=await loadArtifacts(resolve(index));
 await writeFile(join(owned,'control.json'),JSON.stringify({key:secret,url:publicOrigin+'/_fixture-independent/control'})+'\n',{mode:0o600});
 await writeFile(join(owned,'fixture.json'),JSON.stringify({mode:process.env.NEXTGO_INDEPENDENT_ZONES_ARTIFACTS?'prebuilt':'isolated-source-build',index:artifacts.index,ports,hashes:artifacts.hashes,binaryPaths:Object.fromEntries([...Object.entries(artifacts.releases),...Object.entries(artifacts.ingresses)].map(([key,value])=>[key,value.binary])),releases:Object.fromEntries(Object.entries(artifacts.releases).map(([key,value])=>[key,value.manifest])),ingresses:Object.fromEntries(Object.entries(artifacts.ingresses).map(([key,value])=>[key,value.manifest]))},null,2)+'\n');
 for(const id of ['A1','A2','B1']){const hosts=id==='B1'?[ports.B1]:[ports[id],ports.relay];const item=processes.start(artifacts.releases[id].binary,['-addr',`127.0.0.1:${ports[id]}`,'-backend-hosts',hosts.map(port=>`127.0.0.1:${port}`).join(',')],owned);await waitPort(ports[id],item);}
 relay=server((req,res)=>{if(!legal(req,ports.relay)){fail(res,400,'Invalid relay authority/path');return;}const target=relayMode==='miswire'?'A1':'A2';forward(req,res,ports[target],'relay',{target});});await listen(relay,ports.relay);
 for(const id of ['I1','I2','rollback']){const item=processes.start(artifacts.ingresses[id].binary,['-addr',`127.0.0.1:${ports[id]}`],owned);await waitPort(ports[id],item);}
 front=server((req,res)=>{if(!legal(req,ports.front)){fail(res,400,'Invalid fixture authority/path');return;}if(req.url.startsWith('/_fixture-independent/')){void control(req,res).catch(()=>fail(res,400,'Invalid fixture control request'));return;}forward(req,res,ports[snapshot],'front');});await listen(front,ports.front);
 for(const item of processes.children)item.done.then(result=>{if(!stopping&&result.code!==0){console.error('Owned zone process exited',result);void stop(1);}});
}catch(error){console.error(error);await stop(1);}
