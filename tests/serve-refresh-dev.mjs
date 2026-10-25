// Source edits, generated output and compiler inputs belong to this isolated
// framework copy. This fixture never writes the checkout's Go inputs.
import {spawn,execFileSync} from 'node:child_process';
import {cp,mkdir,readdir,readFile,writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {join,resolve,relative} from 'node:path';
import {connect} from 'node:net';

const root=process.cwd();
if(!process.env.NEXTGO_REFRESH_DEV_ROOT)throw new Error('NEXTGO_REFRESH_DEV_ROOT is required');
const owned=resolve(process.env.NEXTGO_REFRESH_DEV_ROOT),framework=join(owned,'framework'),app=join(framework,'examples/refresh');
await mkdir(owned,{recursive:true});
const excluded=new Set(['node_modules','dist','docs','tests','examples','test-results','test-results-crossbrowser','playwright-report']);
function allowed(path){return !relative(root,path).split(/[\\/]/).some(part=>part.startsWith('.'))&&!path.endsWith('_test.go');}
for(const entry of await readdir(root,{withFileTypes:true})){
 if(entry.name.startsWith('.')||excluded.has(entry.name))continue;
 if(entry.isFile()&&!entry.name.endsWith('.go')&&!['go.mod','go.sum'].includes(entry.name))continue;
 if(!entry.isFile()&&!entry.isDirectory())throw new Error('Unexpected framework input type');
 await cp(join(root,entry.name),join(framework,entry.name),{recursive:true,filter:allowed});
}
await cp(join(root,'examples/refresh'),app,{recursive:true,filter:path=>!path.split(/[\\/]/).some(part=>part==='.nextgo')&&!path.endsWith('_test.go')});
const files={counter:'examples/refresh/components/refreshcounter/client_js.go',schema:'examples/refresh/components/refreshcounter/component.json',props:'examples/refresh/components/refreshcounter/props.go',native:'examples/refresh/serverstate/state.go',shared:'examples/refresh/shared/message.go',eager:'examples/refresh/components/eagerbadge/client_js.go',core:'client/runtime_js.go'};
const originals={};for(const [key,path]of Object.entries(files))originals[key]=await readFile(join(framework,path),'utf8');
await writeFile(join(owned,'originals.json'),JSON.stringify({files,originals},null,2)+'\n');
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
async function inventory(directory){const records=[];async function walk(dir){for(const entry of (await readdir(dir,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){const path=join(dir,entry.name);if(entry.isDirectory())await walk(path);else if(entry.isFile()){const bytes=await readFile(path);records.push({path:relative(directory,path).split('\\').join('/'),bytes:bytes.length,sha256:sha(bytes)});}else throw new Error('Unexpected copied input type');}}await walk(directory);return {files:records.length,sha256:sha(JSON.stringify(records)),records};}
const inputs=await inventory(framework);
const inherited=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('NEXTGO_REFRESH_')&&!key.startsWith('NEXTGO_ZONE_')&&!key.startsWith('NEXTGO_DEMO_')&&!key.startsWith('NEXTGO_LAMBDA_')&&!key.startsWith('AWS_LAMBDA_')&&!['NEXTGO_ENV_DIR','NEXTGO_ENV_MODE'].includes(key)));
const env={...inherited,GOTOOLCHAIN:'local',GOWORK:'off',GOMAXPROCS:'1',GOFLAGS:inherited.GOFLAGS||'-p=1'};
const provenance={inputs,sdk:execFileSync('go',['version'],{encoding:'utf8',env}).trim(),compiler:{GOMAXPROCS:env.GOMAXPROCS,GOFLAGS:env.GOFLAGS,GOWORK:env.GOWORK,GOTOOLCHAIN:env.GOTOOLCHAIN},scope:'Actual development CLI and Go/WASM in an isolated framework/sample copy. Native production binary equivalence and performance are not asserted.'};
let current,stopping=false,cleanupPromise,parentWatch,log='';const nativePorts=new Set();
function own(command,args,cwd){const child=spawn(command,args,{cwd,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});const chunk=bytes=>{const text=bytes.toString();process.stdout.write(text);if(log.length<2<<20)log+=text;for(const match of text.matchAll(/listening on (?:http:\/\/)?127\.0\.0\.1:(\d+)/g))nativePorts.add(Number(match[1]));};child.stdout.on('data',chunk);child.stderr.on('data',chunk);const done=new Promise(resolve=>{child.once('error',error=>resolve({error:String(error)}));child.once('close',(code,signal)=>resolve({code,signal}));});return {child,done};}
async function closed(port){return new Promise(resolve=>{const socket=connect({host:'127.0.0.1',port});let settled=false;const finish=value=>{if(settled)return;settled=true;socket.destroy();resolve(value);};socket.once('connect',()=>finish(false));socket.once('error',error=>finish(error.code==='ECONNREFUSED'));socket.setTimeout(500,()=>finish(false));});}
async function outputDirectories(){try{return (await readdir(join(app,'.nextgo'),{withFileTypes:true})).filter(entry=>entry.isDirectory()&&entry.name.startsWith('build-')).map(entry=>entry.name);}catch(error){if(error.code==='ENOENT')return [];throw error;}}
function stop(code=0){if(cleanupPromise)return cleanupPromise;stopping=true;clearInterval(parentWatch);cleanupPromise=(async()=>{let result;if(current){current.child.kill('SIGTERM');let timer;result=await Promise.race([current.done,new Promise(resolve=>{timer=setTimeout(()=>resolve({timeout:true}),12_000);})]);clearTimeout(timer);if(result.timeout){try{if(process.platform==='win32')current.child.kill('SIGKILL');else process.kill(-current.child.pid,'SIGKILL');}catch{}await current.done;}}
 const remaining=await outputDirectories(),listeners=await Promise.all([...new Set([...nativePorts,4240])].map(async port=>({port,closed:await closed(port)})));
 const evidence={stoppedAt:new Date().toISOString(),cliExit:result,remainingCompilerOutput:remaining,listeners,cleanupPass:!result?.timeout&&remaining.length===0&&listeners.every(value=>value.closed)};
 await writeFile(join(owned,'cleanup.json'),JSON.stringify(evidence,null,2)+'\n');await writeFile(join(owned,'supervisor.log'),log);
 if(!evidence.cleanupPass){console.error('refresh development cleanup failed',JSON.stringify(evidence));process.exitCode=1;}else process.exitCode=code;
})();return cleanupPromise;}
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{void stop();});
const parentPID=process.ppid;parentWatch=setInterval(()=>{let lost=process.ppid!==parentPID;try{process.kill(parentPID,0);}catch(error){if(error.code==='ESRCH')lost=true;}if(lost)void stop(1);},1000);
try{
 const supplied=process.env.NEXTGO_E2E_NEXTGO_BINARY;let cli=join(owned,'nextgo');
 if(supplied){const before=sha(await readFile(supplied));await cp(supplied,cli);const after=sha(await readFile(cli));if(before!==after)throw new Error('Supplied CLI copy changed');provenance.suppliedCLI={path:resolve(supplied),sha256:before,copiedSHA256:after};}
 else{current=own('go',['build','-o',cli,'./cmd/nextgo'],framework);const result=await current.done;if(result.code!==0||stopping)throw new Error('Copied CLI build failed');}
 provenance.cliSHA256=sha(await readFile(cli));provenance.cliBuildInfo=execFileSync('go',['version','-m',cli],{encoding:'utf8',env}).trim();await writeFile(join(owned,'provenance.json'),JSON.stringify(provenance,null,2)+'\n');
 if(stopping)throw new Error('Fixture stopped before development launch');
 current=own(cli,['dev','-addr','127.0.0.1:4240',app],framework);const result=await current.done;if(!stopping){console.error('Development CLI exited unexpectedly',result);await stop(1);}
}catch(error){console.error(error);await stop(1);}
