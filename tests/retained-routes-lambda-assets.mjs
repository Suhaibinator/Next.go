// Qualification-only actual ZIP asset transport. This module launches no
// process, supplies no application response and borrows no CGO1 identities.
import {createHash} from 'node:crypto';
import {request as httpRequest} from 'node:http';
import {gunzipSync} from 'node:zlib';

const MiB=1<<20,magic='0061736d01000000';
const limits=Object.freeze({html:MiB,wasm:64*MiB,module:8*MiB,script:MiB,css:256<<10,headers:16<<10,headerCount:64,headerValue:8<<10,milliseconds:16_000});
const sha=data=>createHash('sha256').update(data).digest('hex');
const hex=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
const check=(valid,message)=>{if(!valid)throw Error('ZIP asset proof: '+message);};
const appID=app=>typeof app==='string'?app:app?.id;
function originURL(origin,path){
  check(typeof origin==='string'&&origin==='http://127.0.0.1:4188','exact owned native fixture origin required');
  check(typeof path==='string'&&path.startsWith('/')&&!path.startsWith('//')&&!/[\\#\0]/.test(path),'literal owned request path required');
  const url=new URL(path,origin);check(url.origin===origin&&url.pathname===path.split('?',1)[0],'request escaped owned fixture');return url.href;
}
function headerMap(raw){
  check(Array.isArray(raw)&&raw.length%2===0&&raw.length<=limits.headerCount*2,'raw HTTP header count bound');
  const result=Object.create(null),single=new Set(['content-type','content-encoding','content-length','transfer-encoding','connection']);let bytes=0;
  for(let i=0;i<raw.length;i+=2){const name=raw[i].toLowerCase(),value=raw[i+1];
    check(/^[a-z0-9!#$%&'*+.^_`|~-]+$/.test(name)&&typeof value==='string'&&Buffer.byteLength(value)<=limits.headerValue&&!/[\x00-\x08\x0a-\x1f\x7f]/.test(value),'invalid raw HTTP header');
    check(result[name]===undefined||!single.has(name),'duplicate representation/framing header');bytes+=Buffer.byteLength(name)+Buffer.byteLength(value)+4;check(bytes<=limits.headers,'HTTP header byte bound');result[name]=result[name]===undefined?value:result[name]+','+value;
  }return result;
}
export function selectZIPAssets(app,artifact,sdkRuntimeSHA){
  const id=appID(app);check(id==='retained'||id==='merge','explicit app required');check(hex(sdkRuntimeSHA),'actual selected SDK runtime hash required');
  check(artifact&&hex(artifact.sha256)&&/^[a-f0-9]{20}$/.test(artifact.runtimeId||'')&&Array.isArray(artifact.assets)&&artifact.assets.length<=4096,'actual extracted ZIP artifact inventory required');
  const prefix='/_nextgo/'+artifact.runtimeId+'/',seen=new Set(),requests=new Map();
  for(const entry of artifact.assets){
    check(entry&&typeof entry.embeddedPath==='string'&&entry.embeddedPath.length<=4096&&!entry.embeddedPath.startsWith('/')&&!/[\\\0]/.test(entry.embeddedPath)&&entry.embeddedPath.split('/').every(part=>part&&part!=='.'&&part!=='..')&&!seen.has(entry.embeddedPath),'unique literal embedded path required');seen.add(entry.embeddedPath);
    check(Number.isSafeInteger(entry.bytes)&&entry.bytes>=0&&entry.bytes<=128*MiB&&hex(entry.sha256),'bounded actual embedded inventory identity');
    if(entry.defaultRequestPath!==undefined){check(typeof entry.defaultRequestPath==='string'&&entry.defaultRequestPath.startsWith('/')&&!entry.defaultRequestPath.startsWith('//')&&!/[\\?#\0]/.test(entry.defaultRequestPath)&&!requests.has(entry.defaultRequestPath),'unique literal default request path required');requests.set(entry.defaultRequestPath,entry);}
  }
  const descriptors=[];
  function add(name,path,mime,maximum){const entry=requests.get(path);check(entry&&entry.bytes>0&&entry.bytes<=maximum,'actual inventory lacks bounded required asset: '+name);if(name==='wasm_exec.js')check(entry.sha256===sdkRuntimeSHA,'ZIP embedded shim differs from selected compiler SDK');descriptors.push({name,path,mime,maximum,bytes:entry.bytes,sha256:entry.sha256,embeddedPath:entry.embeddedPath});}
  add('app.wasm',prefix+'app.wasm','application/wasm',limits.wasm);add('wasm_exec.js',prefix+'wasm_exec.js','text/javascript',limits.script);add('boot.js',prefix+'boot.js','text/javascript',limits.script);add('css','/assets/'+id+'.css','text/css',limits.css);
  const modules=[...requests.keys()].filter(path=>path.startsWith(prefix)&&path.slice(prefix.length).startsWith('module-'));
  check(modules.length===(id==='merge'?1:0),'expected app-specific generated module inventory');
  for(const path of modules.sort()){const entry=requests.get(path),name=path.slice(prefix.length);check(/^module-[a-f0-9]{64}\.wasm$/.test(name)&&name==='module-'+entry.sha256+'.wasm','actual content-addressed module inventory required');add(name,path,'application/wasm',limits.module);}
  return {app:id,prefix,artifactSHA256:artifact.sha256,descriptors};
}
// The caller owns this HTTP exchange. Success and failure join response,
// request and socket close; absolute deadline destruction is never a join.
export function wireZIPGet(url,encoding,maximum){
  check(typeof url==='string'&&['identity','gzip'].includes(encoding)&&Number.isSafeInteger(maximum)&&maximum>0&&maximum<=limits.wasm+MiB,'bounded raw HTTP exchange arguments');
  const parsed=new URL(url);originURL(parsed.origin,parsed.pathname+parsed.search);
  return new Promise((resolve,reject)=>{let request,response,socket,timer,failure,settled=false,requestClosed=false,responseClosed=false,socketClosed=false,ended=false,headers,bytes=0;const parts=[];
    const finish=()=>{if(settled||!requestClosed||response&&!responseClosed||socket&&!socketClosed)return;if(!failure&&(!response||!ended||!response.complete||!socket))failure=Error('ZIP asset proof: incomplete physical HTTP exchange');settled=true;clearTimeout(timer);if(failure)return reject(failure);resolve({status:response.statusCode,headers,data:Buffer.concat(parts,bytes),bytes,complete:response.complete,closed:true,requestClosed,responseClosed,socketClosed});};
    const fail=error=>{failure||=error instanceof Error?error:Error('ZIP asset proof: raw HTTP failure');response?.destroy(failure);request?.destroy(failure);socket?.destroy(failure);finish();};
    timer=setTimeout(()=>fail(Error('ZIP asset proof: absolute HTTP deadline exceeded')),limits.milliseconds);
    try{request=httpRequest(url,{method:'GET',agent:false,maxHeaderSize:limits.headers,headers:{'accept-encoding':encoding,connection:'close'}},value=>{
      response=value;response.once('error',fail);response.once('aborted',()=>fail(Error('ZIP asset proof: response aborted')));response.once('close',()=>{responseClosed=true;if(!ended&&!failure)fail(Error('ZIP asset proof: response closed before end'));finish();});response.once('end',()=>{ended=true;if(!response.complete)fail(Error('ZIP asset proof: incomplete response framing'));finish();});response.on('data',part=>{if(failure)return;bytes+=part.length;if(bytes>maximum)return fail(Error('ZIP asset proof: response byte bound'));parts.push(part);});try{headers=headerMap(response.rawHeaders);}catch(error){fail(error);}
    });request.once('error',fail);request.once('close',()=>{requestClosed=true;if(!response&&!failure)fail(Error('ZIP asset proof: request closed without response'));finish();});request.once('socket',value=>{socket=value;socket.once('error',fail);socket.once('close',()=>{socketClosed=true;finish();});if(failure)socket.destroy(failure);});request.end();}
    catch(error){if(!request){settled=true;clearTimeout(timer);reject(error);}else fail(error);}
  });
}
export function validateZIPExchange(wire,descriptor,encoding){
  check(wire&&wire.status===200&&wire.complete===true&&wire.closed===true&&wire.requestClosed===true&&wire.responseClosed===true&&wire.socketClosed===true&&Buffer.isBuffer(wire.data),'actual complete physical HTTP exchange required');
  const {headers,data}=wire;check(headers&&headers['content-type']?.split(';',1)[0].trim().toLowerCase()===descriptor.mime,'actual asset MIME mismatch');
  if(headers['content-length']!==undefined){check(/^(0|[1-9][0-9]{0,9})$/.test(headers['content-length'])&&Number(headers['content-length'])===data.length,'canonical actual content length required');}
  const actual=headers['content-encoding']?.trim().toLowerCase();check(encoding==='gzip'?actual==='gzip':!actual||actual==='identity','actual HTTP encoding mismatch');
  const vary=headers.vary?.toLowerCase().split(',').map(value=>value.trim());check(vary&&vary.length<=32&&vary.includes('accept-encoding'),'actual Vary Accept-Encoding required');
  check(data.length>0&&data.length<=descriptor.maximum+MiB,'actual wire size bound');
  if(encoding==='gzip')check(data.subarray(0,3).toString('hex')==='1f8b08','actual gzip wire magic required');
  const decoded=encoding==='gzip'?gunzipSync(data,{maxOutputLength:descriptor.maximum}):data;
  check(decoded.length===descriptor.bytes&&sha(decoded)===descriptor.sha256,'actual asset bytes differ from own ZIP deployment inventory');
  if(descriptor.mime==='application/wasm')check(decoded.subarray(0,8).toString('hex')===magic,'actual decoded WASM magic required');
  return {path:descriptor.path,bytes:decoded.length,sha256:sha(decoded),headers,mime:descriptor.mime,embeddedPath:descriptor.embeddedPath,wireBytes:data.length,wireSHA256:sha(data),decodedBytes:decoded.length,decodedSHA256:sha(decoded),complete:true,closed:true};
}
function sameBaseline(baseline,selection){
  check(baseline?.app===selection.app&&baseline.prefix===selection.prefix&&baseline.artifactSHA256===selection.artifactSHA256&&baseline.assets&&Object.keys(baseline.assets).length===selection.descriptors.length,'own per-app CGO0 ZIP HTTP baseline required');
  for(const expected of selection.descriptors){const actual=baseline.assets[expected.name];check(actual&&actual.path===expected.path&&actual.sha256===expected.sha256&&actual.bytes===expected.bytes&&actual.embeddedPath===expected.embeddedPath&&actual.mime===expected.mime,'HTTP baseline must match actual same-app ZIP inventory');}
}
export function expectedAssetErrors(baseline,mode){
  check(mode==='buffered'||mode==='streaming','explicit RuntimeAPI mode required');check(baseline&&baseline.assets&&Object.keys(baseline.assets).length<=16,'bounded own asset baseline required');
  return mode==='streaming'?[]:Object.values(baseline.assets).filter(asset=>asset.mime==='application/wasm'&&asset.bytes>4*MiB).map(asset=>({path:asset.path,message:'HTTP response exceeds buffered Lambda payload limit',type:'NextGoInvocationError',class:'buffered-overflow'}));
}
export function validateZIPRuntimeReceipt(report,wire,mode,kind='response'){
  check(report&&wire&&Buffer.isBuffer(wire.data)&&['buffered','streaming'].includes(mode)&&['response','error'].includes(kind),'explicit actual Runtime response/error exchange required');
  check(report.kind===kind&&report.ack===202,'actual RuntimeAPI report kind/ACK');
  check(report.mode===mode&&Number.isSafeInteger(report.runtimeBytes)&&report.runtimeBytes>0,'actual same-transport publication byte count');
  if(kind==='response'){
    check(report.statusCode===wire.status&&report.httpBytes===wire.data.length,'actual HTTP status and raw encoded body agree with Runtime publication');
    if(mode==='buffered'){
      check(report.runtimeBytes<=6*MiB&&report.rawJSONBytes===report.runtimeBytes&&report.httpBytes<=4*MiB&&report.metadataBytes===0&&typeof report.base64flag==='boolean','actual full buffered JSON/body/metadata bounds');
      const encoded=report.base64flag?Math.ceil(report.httpBytes/3)*4:report.httpBytes;check(report.rawJSONBytes>=encoded,'actual full buffered JSON includes actual encoded entity');
      if(wire.headers['content-encoding']==='gzip')check(report.base64flag===true,'buffered gzip must actually use base64 envelope');
    }else check(report.httpBytes<=32*MiB&&Number.isSafeInteger(report.metadataBytes)&&report.metadataBytes>0&&report.metadataBytes<=16<<10&&report.rawJSONBytes===report.metadataBytes&&report.runtimeBytes===report.metadataBytes+8+report.httpBytes&&report.base64flag===null,'actual streamed metadata/delimiter/entity byte accounting');
  }else check(wire.status===502&&mode==='buffered'&&report.errorMessage==='HTTP response exceeds buffered Lambda payload limit'&&report.errorType==='NextGoInvocationError'&&report.runtimeBytes<=64<<10&&report.rawJSONBytes===report.runtimeBytes&&report.httpBytes===0&&report.metadataBytes===0&&report.base64flag===null&&report.statusCode===null,'actual bounded Go buffered-overflow Runtime/error payload required');
  return report;
}
async function runtimeReceipt(native,before,path,wire,mode,kind='response'){
  await native.waitFor(snapshot=>snapshot.reports.slice(before).some(item=>item.path===path&&item.kind===kind&&item.ack===202));
  const reports=native.snapshot().lambda.reports.slice(before).filter(item=>item.path===path);check(reports.length===1,'one new actual RuntimeAPI report for this asset flight');return validateZIPRuntimeReceipt(reports[0],wire,mode,kind);
}
export async function probeZIPAssets(native,app,artifact,sdkRuntimeSHA,{mode='http',baseline}={}){
  check(['http','buffered','streaming'].includes(mode),'explicit actual transport required');const selection=selectZIPAssets(app,artifact,sdkRuntimeSHA),id=selection.app,transport=mode!=='http';if(transport)sameBaseline(baseline,selection);
  const result={app:id,prefix:selection.prefix,artifactSHA256:selection.artifactSHA256,transport:mode,ssr:null,assets:{},modules:[],identity:{},expectedInvocationErrors:[]};
  const initial=id==='retained'?'/a':'/?q=a',html=await wireZIPGet(originURL(native.origin,initial),'identity',limits.html);
  check(html.status===200&&html.headers['content-type']?.split(';',1)[0].trim().toLowerCase()==='text/html'&&!html.headers['content-encoding'],'actual initial SSR HTML before assets required');
  const text=new TextDecoder('utf-8',{fatal:true}).decode(html.data),prefixes=[...text.matchAll(/\bsrc="(\/_nextgo\/[a-f0-9]{20}\/)wasm_exec\.js"/g)].map(match=>match[1]),boots=[...text.matchAll(/\bsrc="(\/_nextgo\/[a-f0-9]{20}\/)boot\.js"/g)].map(match=>match[1]);check(prefixes.length===1&&prefixes[0]===selection.prefix&&boots.length===1&&boots[0]===selection.prefix,'actual SSR and ZIP share one runtime prefix');
  result.ssr={path:initial,bytes:html.bytes,sha256:sha(html.data),prefix:selection.prefix,complete:true,closed:true};
  for(const descriptor of selection.descriptors){
    const maximum=transport?(mode==='buffered'?4*MiB:32*MiB):descriptor.maximum+MiB,before=transport?native.snapshot().lambda.reports.length:0;
    const gzip=await wireZIPGet(originURL(native.origin,descriptor.path),'gzip',maximum),actual=validateZIPExchange(gzip,descriptor,'gzip');if(transport)actual.runtime=await runtimeReceipt(native,before,descriptor.path,gzip,mode);
    result.assets[descriptor.name]={path:descriptor.path,bytes:descriptor.bytes,sha256:descriptor.sha256,headers:actual.headers,mime:descriptor.mime,embeddedPath:descriptor.embeddedPath,gzip:actual};
    const oversized=mode==='buffered'&&descriptor.mime==='application/wasm'&&descriptor.bytes>4*MiB;
    if(oversized){native.expectAssetOverflow(descriptor.path);const start=native.snapshot().lambda.reports.length,wire=await wireZIPGet(originURL(native.origin,descriptor.path),'identity',64<<10);check(wire.status===502&&wire.complete&&wire.closed,'oversized identity requires actual RuntimeAPI/error HTTP502');const runtime=await runtimeReceipt(native,start,descriptor.path,wire,mode,'error');result.identity[descriptor.name]={outcome:'REAL_RUNTIME_ERROR_NOT_IDENTITY_DEPLOYABILITY',status:wire.status,runtime};}
    else{const start=transport?native.snapshot().lambda.reports.length:0,wire=await wireZIPGet(originURL(native.origin,descriptor.path),'identity',descriptor.maximum+MiB),identity=validateZIPExchange(wire,descriptor,'identity');if(transport)identity.runtime=await runtimeReceipt(native,start,descriptor.path,wire,mode);result.identity[descriptor.name]={outcome:'ACTUAL_IDENTITY_BYTES',...identity};}
    if(descriptor.name.startsWith('module-'))result.modules.push(result.assets[descriptor.name]);
  }
  result.expectedInvocationErrors=mode==='http'?[]:expectedAssetErrors(result,mode);return result;
}
export function validateZIPModuleProof(value,baseline){
  check(baseline?.app==='merge'&&Array.isArray(baseline.modules)&&baseline.modules.length===1,'own merge ZIP module baseline required');const expected=baseline.modules[0];
  check(value&&value.version===1&&value.abi===6&&Array.isArray(value.records)&&value.records.length===2&&Buffer.byteLength(JSON.stringify(value))<=4096,'bounded actual browser module records required');
  for(const [index,record]of value.records.entries()){
    check(record.phase===['initial','held-recovery'][index]&&record.status===200&&record.mime==='application/wasm'&&record.complete===true&&record.wasmMagic===magic&&record.hostABI===6&&record.buildPrefix===baseline.prefix,'actual same-ZIP module runtime/response binding');
    check(record.url===originURL('http://127.0.0.1:4188',expected.path)&&record.bytes===expected.bytes&&record.sha256===expected.sha256,'browser module must match actual own CGO0 ZIP inventory and HTTP baseline');
  }return {sha256:expected.sha256,bytes:expected.bytes,abi:6,buildPrefix:baseline.prefix,downloads:2};
}
