// Every editable Go file and every watched local framework input belongs to
// this fixture's copies. The frozen checkout is read only throughout the run.
import {spawn, execFileSync} from 'node:child_process';
import {cp, mkdir, readdir, readFile, writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {join, resolve, relative} from 'node:path';
import {connect} from 'node:net';

const root=process.cwd();
const owned=resolve(process.env.NEXTGO_ZONES_DEV_ROOT);
const framework=join(owned,'framework'), app=join(owned,'application');
const module='example.test/nextgo-zones-development';
await mkdir(owned,{recursive:true});
const excluded=new Set(['node_modules','dist','docs','tests','examples','test-results','test-results-crossbrowser','playwright-report']);
function allowed(path){const parts=relative(root,path).split(/[\\/]/);return !parts.some(p=>p.startsWith('.'))&&!path.endsWith('_test.go');}
for(const entry of await readdir(root,{withFileTypes:true})){
 if(entry.name.startsWith('.')||excluded.has(entry.name))continue;
 if(entry.isFile()&&!entry.name.endsWith('.go')&&!['go.mod','go.sum'].includes(entry.name))continue;
 if(!entry.isFile()&&!entry.isDirectory())throw new Error('Unexpected framework source symlink');
 await cp(join(root,entry.name),join(framework,entry.name),{recursive:true,filter:allowed});
}
for(const name of ['marketing','blog','site'])await cp(join(root,'examples/zones',name),join(app,name),{recursive:true,filter:path=>!path.split(/[\\/]/).includes('.nextgo')&&!path.endsWith('_test.go')});
async function rewrite(directory){for(const entry of await readdir(directory,{withFileTypes:true})){const path=join(directory,entry.name);if(entry.isDirectory())await rewrite(path);else if(entry.name.endsWith('.go')){const text=await readFile(path,'utf8');await writeFile(path,text.replaceAll('github.com/Suhaibinator/Next.go/examples/zones/',module+'/'));}}}
await rewrite(app);
await writeFile(join(app,'go.mod'),`module ${module}\n\ngo 1.24\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => ${JSON.stringify(framework)}\n`);
const manifest={version:1,zones:[{id:'marketing',host:'127.0.0.1:4236',basePath:'',app:'./marketing'},{id:'blog',host:'127.0.0.1:4236',basePath:'/blog',app:'./blog'}]};
await writeFile(join(app,'zones.json'),JSON.stringify(manifest,null,2)+'\n');
const counter=join(app,'marketing/components/counter/client_js.go'), style=join(app,'marketing/public/style.css');
const originals={counter:await readFile(counter,'utf8'),style:await readFile(style,'utf8'),manifest:JSON.stringify(manifest,null,2)+'\n'};
await writeFile(join(owned,'originals.json'),JSON.stringify(originals));
const sha=b=>createHash('sha256').update(b).digest('hex');
async function inventory(directory){const records=[];async function walk(dir){for(const entry of (await readdir(dir,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){const path=join(dir,entry.name);if(entry.isDirectory())await walk(path);else if(entry.isFile())records.push({path:relative(directory,path).split('\\').join('/'),bytes:(await readFile(path)).length,sha256:sha(await readFile(path))});else throw new Error('Unexpected copied input type');}}await walk(directory);return {files:records.length,sha256:sha(Buffer.from(JSON.stringify(records))),records};}
const frameworkInputs=await inventory(framework), applicationInputs=await inventory(app);
const base=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('NEXTGO_ZONE_')&&!key.startsWith('NEXTGO_DEMO_')&&!key.startsWith('NEXTGO_LAMBDA_')&&!key.startsWith('AWS_LAMBDA_')&&!['NEXTGO_ENV_DIR','NEXTGO_ENV_MODE'].includes(key)));
const env={...base,GOTOOLCHAIN:'local',GOWORK:'off',GOMAXPROCS:base.GOMAXPROCS||'2',GOFLAGS:base.GOFLAGS||'-p=2'};
let current, stopping=false, cleanupPromise, parentWatch;
const nativePorts=new Set();let log='';
const logChunk=chunk=>{const text=chunk.toString();process.stdout.write(text);if(log.length<2<<20)log+=text;for(const match of text.matchAll(/Next\.go zones listening on 127\.0\.0\.1:(\d+)/g))nativePorts.add(Number(match[1]));};
function own(command,args,cwd){const child=spawn(command,args,{cwd,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});child.stdout.on('data',logChunk);child.stderr.on('data',logChunk);const done=new Promise(resolve=>{child.once('error',error=>resolve({error:String(error)}));child.once('close',(code,signal)=>resolve({code,signal}));});return {child,done};}
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function closed(port){return new Promise(resolve=>{const socket=connect({host:'127.0.0.1',port});let settled=false;const finish=value=>{if(settled)return;settled=true;socket.destroy();resolve(value);};socket.once('connect',()=>finish(false));socket.once('error',error=>finish(error.code==='ECONNREFUSED'));socket.setTimeout(500,()=>finish(false));});}
async function outputs(){const paths=[];for(const directory of [app,join(app,'marketing'),join(app,'blog')]){try{for(const entry of await readdir(join(directory,'.nextgo'),{withFileTypes:true}))if(entry.isDirectory()&&entry.name.startsWith('build-'))paths.push(join(directory,'.nextgo',entry.name));}catch(error){if(error.code!=='ENOENT')throw error;}}return paths;}
function stop(code=0){if(cleanupPromise)return cleanupPromise;stopping=true;clearInterval(parentWatch);cleanupPromise=(async()=>{let result;if(current){current.child.kill('SIGTERM');let timer;result=await Promise.race([current.done,new Promise(resolve=>{timer=setTimeout(()=>resolve({timeout:true}),12_000);})]);clearTimeout(timer);if(result.timeout){try{if(process.platform==='win32')current.child.kill('SIGKILL');else process.kill(-current.child.pid,'SIGKILL');}catch{}await current.done;}}
 for(const match of log.matchAll(/Next\.go zones listening on 127\.0\.0\.1:(\d+)/g))nativePorts.add(Number(match[1]));
 const remaining=await outputs(), ports=await Promise.all([...nativePorts,4236].map(async port=>({port,closed:await closed(port)})));
 const evidence={...provenance,stoppedAt:new Date().toISOString(),cliExit:result,remainingCompilerOutput:remaining,nativeListeners:ports,cleanupPass:!result?.timeout&&remaining.length===0&&ports.every(p=>p.closed)};
 await writeFile(join(owned,'cleanup.json'),JSON.stringify(evidence,null,2)+'\n');await writeFile(join(owned,'supervisor.log'),log);
 if(!evidence.cleanupPass){console.error('zones development cleanup failed',JSON.stringify(evidence));process.exitCode=1;}else process.exitCode=code;
})();return cleanupPromise;}
let cli=process.env.NEXTGO_E2E_NEXTGO_BINARY;
let provenance={frameworkInputs,applicationInputs,sdk:execFileSync('go',['version'],{encoding:'utf8',env}).trim(),workspaceMode:'off (isolated fixture module)',compilerParallelism:{GOMAXPROCS:env.GOMAXPROCS,GOFLAGS:env.GOFLAGS},scope:'Actual zones dev CLI against isolated framework/sample source copies. No checkout Go inputs or public assets are edited. No performance or production artifact equivalence claim.'};
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{void stop();});
const parentPID=process.ppid;parentWatch=setInterval(()=>{let lost=process.ppid!==parentPID;try{process.kill(parentPID,0);}catch(error){if(error.code==='ESRCH')lost=true;}if(lost)void stop(1);},1000);
try{
 if(!cli){cli=join(owned,'nextgo');current=own('go',['build','-o',cli,'./cmd/nextgo'],framework);const result=await current.done;if(result.code!==0||stopping)throw new Error('Copied CLI build did not complete');}
 provenance.cliSHA256=sha(await readFile(cli));provenance.cliBuildInfo=execFileSync('go',['version','-m',cli],{encoding:'utf8',env}).trim();
 await writeFile(join(owned,'provenance.json'),JSON.stringify(provenance,null,2)+'\n');
 if(stopping)throw new Error('Fixture stopped before dev launch');
 current=own(cli,['zones','dev','-addr','127.0.0.1:4236',join(app,'zones.json')],app);
 const result=await current.done;
 if(!stopping){console.error('zones development CLI exited unexpectedly',result);await stop(1);}
}catch(error){console.error(error);await stop(1);}
