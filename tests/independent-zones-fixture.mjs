// This is test orchestration, not a deployment or a public management API.
// Artifact manifests and binaries are trusted operator inputs. Every app and
// compatibility response comes from the actual Go release/ingress binaries.
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {cp,mkdir,readdir,readFile,writeFile,open} from 'node:fs/promises';
import {join,resolve,dirname,relative} from 'node:path';
import {connect} from 'node:net';
import {fileURLToPath} from 'node:url';

export const publicOrigin='http://127.0.0.1:4260';
export const ports={front:4260,A1:4261,A2:4262,B1:4263,relay:4264,I1:4265,I2:4266,rollback:4267};
export const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
export const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
export async function readBounded(path,maximum){
 const file=await open(path,'r');try{const info=await file.stat();if(!info.isFile()||info.size>maximum)throw new Error('Artifact file exceeds fixture bound');const data=Buffer.alloc(info.size);let at=0;while(at<data.length){const value=await file.read(data,at,data.length-at,at);if(!value.bytesRead)throw new Error('Artifact file truncated');at+=value.bytesRead;}if((await file.read(Buffer.alloc(1),0,1,at)).bytesRead)throw new Error('Artifact file grew');return data;}finally{await file.close();}
}

export async function inventory(root){
 const files=[];
 async function walk(dir){for(const entry of (await readdir(dir,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){const path=join(dir,entry.name);if(entry.isDirectory())await walk(path);else if(entry.isFile()){const data=await readFile(path);files.push({path:relative(root,path).split('\\').join('/'),bytes:data.length,sha256:sha(data)});}else throw new Error('Unexpected source symlink');}}
 await walk(root);return {count:files.length,sha256:sha(JSON.stringify(files)),files};
}

export function ownProcesses(env){
 const children=[];let stopping=false,closing;
 function start(command,args,cwd){
  if(stopping)throw new Error('Fixture is stopping');
  const child=spawn(command,args,{cwd,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});
  const item={child,command,args,closed:false,log:''};children.push(item);
  for(const stream of [child.stdout,child.stderr])stream.on('data',data=>{process.stdout.write(data);if(item.log.length<256*1024)item.log+=data.toString();});
  item.done=new Promise(resolve=>{child.once('error',error=>{item.closed=true;resolve({error:String(error)});});child.once('close',(code,signal)=>{item.closed=true;resolve({code,signal});});});
  return item;
 }
 async function command(command,args,cwd){const item=start(command,args,cwd),result=await item.done;if(result.code!==0)throw new Error(`Owned command failed (${result.code??result.error})`);return item;}
 function close(){if(closing)return closing;stopping=true;closing=(async()=>{
  const active=children.filter(item=>!item.closed);for(const item of active){try{if(process.platform==='win32')item.child.kill('SIGTERM');else process.kill(-item.child.pid,'SIGTERM');}catch{}}
  let timer;await Promise.race([Promise.all(active.map(item=>item.done)),new Promise(resolve=>{timer=setTimeout(resolve,6500);})]);clearTimeout(timer);
  for(const item of active.filter(item=>!item.closed)){try{if(process.platform==='win32')item.child.kill('SIGKILL');else process.kill(-item.child.pid,'SIGKILL');}catch{}}
  await Promise.all(active.map(item=>item.done));
  return children.map(item=>({command:item.command,args:item.args,exit:item.child.exitCode,signal:item.child.signalCode,closed:item.closed,log:item.log}));
 })();return closing;}
 return {start,command,close,children};
}

export async function waitPort(port,processItem){
 const end=Date.now()+15_000;
 while(Date.now()<end){if(processItem?.closed)throw new Error('Owned server exited before readiness');if(await portOpen(port))return;await delay(50);}
 throw new Error(`Loopback port ${port} did not open`);
}
export async function portOpen(port){return new Promise(resolve=>{const socket=connect({host:'127.0.0.1',port});let done=false;const finish=value=>{if(done)return;done=true;socket.destroy();resolve(value);};socket.once('connect',()=>finish(true));socket.once('error',()=>finish(false));socket.setTimeout(300,()=>finish(false));});}

export function ingressManifest(A1,A2,B1,stage){
 const alpha=stage==='I1'?[{artifact:A1,origin:`http://127.0.0.1:${ports.A1}`}]:[{artifact:A1,origin:`http://127.0.0.1:${ports.A1}`},{artifact:A2,origin:`http://127.0.0.1:${ports.relay}`}];
 return {version:1,topologySHA256:A1.release.topologySHA256,zones:[{binding:A1.release.zone,activeRelease:stage==='I2'?'A2':'A1',releases:alpha},{binding:B1.release.zone,activeRelease:'B1',releases:[{artifact:B1,origin:`http://127.0.0.1:${ports.B1}`}]}]};
}

export async function loadArtifacts(indexPath){
 const index=JSON.parse((await readBounded(indexPath,64<<10)).toString('utf8'));if(index.version!==1||!index.binarySHA256||Array.isArray(index.binarySHA256))throw new Error('Unknown or unpinned artifact index');
 const pinned=(key,data)=>{if(!/^[a-f0-9]{64}$/.test(index.binarySHA256[key]||'')||index.binarySHA256[key]!==sha(data))throw new Error(`Pinned ${key} binary differs`);};
 const releases={},ingresses={},hashes={};
 for(const [key,id,zone,base]of [['releaseA1','A1','alpha',''],['releaseA2','A2','alpha',''],['releaseB1','B1','blog','/blog']]){
  if(typeof index[key]!=='string')throw new Error(`Missing ${key}`);
  const directory=resolve(dirname(indexPath),index[key]),binary=join(directory,'server'),data=await readBounded(binary,256<<20);pinned(key,data);
  const manifest=JSON.parse((await readBounded(join(directory,'zone-release.json'),1<<20)).toString('utf8')),r=manifest.release;
  if(r?.version!==1||r.releaseID!==id||r.zone.id!==zone||r.zone.basePath!==base||r.zone.host!=='127.0.0.1:4260'||r.publicOrigin!==publicOrigin||manifest.binarySHA256!==sha(data))throw new Error(`Release ${id} identity differs from its bytes/profile`);
  releases[id]={directory,binary,manifest};hashes[id]=sha(data);
 }
 const topology=releases.A1.manifest.release.topologySHA256;if(releases.A2.manifest.release.topologySHA256!==topology||releases.B1.manifest.release.topologySHA256!==topology)throw new Error('Release topology differs');
 for(const [key,id]of [['ingressI1','I1'],['ingressI2','I2'],['ingressRollback','rollback']]){
  if(typeof index[key]!=='string')throw new Error(`Missing ${key}`);
  const directory=resolve(dirname(indexPath),index[key]),binary=join(directory,'server'),data=await readBounded(binary,256<<20);pinned(key,data);const actual=JSON.parse((await readBounded(join(directory,'zone-ingress.json'),1<<20)).toString('utf8'));
  const expected=ingressManifest(releases.A1.manifest,releases.A2.manifest,releases.B1.manifest,id);
  const normalize=value=>JSON.stringify({...value,zones:[...value.zones].sort((a,b)=>a.binding.id.localeCompare(b.binding.id)).map(zone=>({...zone,releases:[...zone.releases].sort((a,b)=>a.artifact.release.releaseID.localeCompare(b.artifact.release.releaseID))}))});
  if(normalize(actual)!==normalize(expected))throw new Error(`Ingress ${id} differs from the fixed browser profile`);
  ingresses[id]={directory,binary,manifest:actual};hashes[id]=sha(data);
 }
 return {index:resolve(indexPath),releases,ingresses,hashes};
}

// Only used by the explicit source-build mode. Qualification can instead supply
// a prebuilt index, requiring neither source discovery nor a Go compiler.
export async function buildArtifacts(root,owned,processes,cli){
 const framework=join(owned,'framework'),sample=join(framework,'examples/independent-zones');
 const excluded=new Set(['node_modules','dist','docs','tests','examples','test-results','playwright-report']);
 for(const entry of await readdir(root,{withFileTypes:true})){
  if(entry.name.startsWith('.')||excluded.has(entry.name))continue;
  if(entry.isFile()&&!entry.name.endsWith('.go')&&!['go.mod','go.sum'].includes(entry.name))continue;
  await cp(join(root,entry.name),join(framework,entry.name),{recursive:true,filter:path=>!relative(root,path).split(/[\\/]/).some(part=>part.startsWith('.'))&&!path.endsWith('_test.go')});
 }
 await cp(join(root,'examples/independent-zones'),sample,{recursive:true,filter:path=>!path.split(/[\\/]/).includes('.nextgo')&&!path.endsWith('_test.go')});
 const inputs=await inventory(framework),output=join(owned,'artifacts');await mkdir(output,{recursive:true});
 const copiedCLI=join(owned,'nextgo');
 if(cli){const before=sha(await readFile(cli));await cp(cli,copiedCLI);if(sha(await readFile(copiedCLI))!==before)throw new Error('CLI copy differs');}
 else await processes.command('go',['build','-o',copiedCLI,'./cmd/nextgo'],framework);
 cli=copiedCLI;
 const moduleA=await readFile(join(sample,'apps/alpha/go.mod'),'utf8'),moduleB=await readFile(join(sample,'apps/blog/go.mod'),'utf8');
 if(!moduleA.includes('module independent.example/alpha')||!moduleB.includes('module independent.example/blog'))throw new Error('Expected separate application modules');
 await processes.command(cli,['zones','release','-zone','alpha','-public-origin',publicOrigin,'-release','A1','-out',join(output,'A1'),join(sample,'zones.json')],sample);
 await processes.command(cli,['zones','release','-zone','blog','-public-origin',publicOrigin,'-release','B1','-out',join(output,'B1'),join(sample,'zones.json')],sample);
 const B1before=sha(await readFile(join(output,'B1/server')));
 const edits=[['apps/alpha/internal/state/state.go','const NativeVersion = "alpha-v1"','const NativeVersion = "alpha-v2"'],['apps/alpha/components/counter/client_js.go','const clientProof = "A1"','const clientProof = "A2"'],['apps/alpha/components/counter/client_js.go','const incrementStep = 1','const incrementStep = 2'],['apps/alpha/public/style.css','/* alpha original stylesheet A1 */','/* alpha original stylesheet A2 */'],['apps/alpha/public/style.css','#126d59','#693e8c'],['apps/alpha/public/release.txt','alpha original asset A1','alpha original asset A2']];
 for(const [name,before,after]of edits){const path=join(sample,name),text=await readFile(path,'utf8');if(!text.includes(before))throw new Error(`Missing selected alpha edit token in ${name}`);await writeFile(path,text.replaceAll(before,after));}
 await processes.command(cli,['zones','release','-zone','alpha','-public-origin',publicOrigin,'-release','A2','-out',join(output,'A2'),join(sample,'zones.json')],sample);
 if(sha(await readFile(join(output,'B1/server')))!==B1before)throw new Error('Building A2 changed B1 artifact');
 const A1=JSON.parse(await readFile(join(output,'A1/zone-release.json'),'utf8')),A2=JSON.parse(await readFile(join(output,'A2/zone-release.json'),'utf8')),B1=JSON.parse(await readFile(join(output,'B1/zone-release.json'),'utf8'));
 for(const stage of ['I1','I2','rollback','https-denial']){const name=join(owned,`ingress-${stage}.json`),value=ingressManifest(A1,A2,B1,stage==='https-denial'?'I1':stage);if(stage==='https-denial')for(const zone of value.zones)for(const release of zone.releases)release.artifact=structuredClone(release.artifact),release.artifact.release.publicOrigin='https://127.0.0.1:4260';await writeFile(name,JSON.stringify(value,null,2)+'\n');await processes.command(cli,['zones','ingress-build','-app',join(sample,'apps/alpha'),'-out',join(output,stage),name],sample);}
 const directories={releaseA1:'A1',releaseA2:'A2',releaseB1:'B1',ingressI1:'I1',ingressI2:'I2',ingressRollback:'rollback',ingressHTTPSDenial:'https-denial'},index={version:1,...Object.fromEntries(Object.entries(directories).map(([key,name])=>[key,relative(owned,join(output,name))])),origins:Object.fromEntries(['A1','A2','B1'].map(id=>[id,`http://127.0.0.1:${ports[id]}`])),relayOrigin:`http://127.0.0.1:${ports.relay}`,binarySHA256:{}};
 for(const [key,name]of Object.entries(directories))index.binarySHA256[key]=sha(await readBounded(join(output,name,'server'),256<<20));
 const indexPath=join(owned,'artifact-index.json');await writeFile(indexPath,JSON.stringify(index,null,2)+'\n');
 await writeFile(join(owned,'build-provenance.json'),JSON.stringify({inputs,cliSHA256:sha(await readFile(cli)),selectedAlphaEdits:edits.map(([path,before,after])=>({path,before,after})),B1UnchangedSHA256:B1before,frameworkFinalInputs:await inventory(framework)},null,2)+'\n');return indexPath;
}

if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 if(process.argv[2]!=='build'||!process.argv[3])throw new Error('usage: node tests/independent-zones-fixture.mjs build OWNED_DIRECTORY [QUALIFIED_CLI]');
 const owned=resolve(process.argv[3]);await mkdir(owned,{recursive:true});const processes=ownProcesses({...process.env,GOTOOLCHAIN:'local',GOWORK:'off',GOMAXPROCS:'1',GOFLAGS:process.env.GOFLAGS||'-p=1'});
 try{process.stdout.write(`Artifact index: ${await buildArtifacts(process.cwd(),owned,processes,process.argv[4])}\n`);}finally{await processes.close();}
}
