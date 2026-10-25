// CI-only bounded failure evidence. Never called by the normal test runner.
import fs from 'node:fs/promises';
import {constants} from 'node:fs';
import {createHash,randomUUID} from 'node:crypto';
import {basename,dirname,isAbsolute,join,relative,resolve,sep} from 'node:path';
import {fileURLToPath} from 'node:url';

const markerName='.nextgo-offline-ci-owner.json';
const purpose='managed-offline-ci-failure';
export const offlineCILimits=Object.freeze({totalBytes:512<<20,fileBytes:32<<20,
  metadataBytes:64<<20,profileBytes:384<<20,manifestBytes:1<<20,
  files:8192,scanEntries:32768,depth:64,pathBytes:2048});
const metadataPaths=['report/report.json','build/index.json','build/source-diff.json',
  'build/build-report.json','build/failure.json','build/source-A.json','build/source-B.json',
  ...['A','B'].flatMap(key=>['generate','build'].flatMap(kind=>[
    `build/logs/${key}-${kind}.log`,`build/logs/${key}-${kind}.log.json`]))];
const forbidden=new Set(['sources','artifacts','go-cache','mod-cache','module-cache','node_modules',
  '.git','.aws','.ssh','.codex','.agents']);
const within=(root,value)=>{const r=relative(root,value);return r===''||(!isAbsolute(r)&&r!=='..'&&!r.startsWith('..'+sep));};
const uid=()=>typeof process.getuid==='function'?process.getuid():null;
const absent=async path=>{try{await fs.lstat(path);return false;}catch(e){if(e.code==='ENOENT')return true;throw e;}};
async function realDirectory(path){const s=await fs.lstat(path);if(!s.isDirectory()||s.isSymbolicLink())throw Error('Expected a real directory');return s;}
async function outputPath(value){
  if(!value||!isAbsolute(value)||basename(value)!=='offline-ci-failure')throw Error('Use a new absolute offline-ci-failure directory');
  const parent=await fs.realpath(dirname(value));await realDirectory(parent);
  const out=join(parent,basename(value));if(!await absent(out))throw Error('Failure destination exists; nothing was changed');return out;
}
export async function initializeOfflineCIArtifacts({ownedRoot,destination}){
  if(!ownedRoot||!isAbsolute(ownedRoot))throw Error('An absolute owned root is required');
  const rootInfo=await realDirectory(ownedRoot),root=await fs.realpath(ownedRoot);
  if(!basename(root).startsWith('nextgo-offline-ci.')||(rootInfo.mode&0o077)!==0||rootInfo.uid!==uid())throw Error('Root must be a new private job-owned nextgo-offline-ci directory');
  if((await fs.readdir(root)).length!==0)throw Error('Ownership may be initialized only in an empty directory');
  const out=await outputPath(destination);if(within(root,out)||within(out,root))throw Error('Failure output and owned root must be separate');
  const marker={version:1,purpose,root,destination:out,uid:uid(),nonce:randomUUID()};
  const file=await fs.open(join(root,markerName),'wx',0o400);
  try{await file.writeFile(JSON.stringify(marker)+'\n');await file.chmod(0o400);await file.sync();}finally{await file.close();}
  return marker;
}
async function ownedMarker(ownedRoot){
  if(!ownedRoot||!isAbsolute(ownedRoot))throw Error('An absolute owned root is required');
  const info=await realDirectory(ownedRoot),root=await fs.realpath(ownedRoot);
  if(!basename(root).startsWith('nextgo-offline-ci.')||(info.mode&0o077)!==0||info.uid!==uid())throw Error('Refused a foreign/nonprivate root');
  const path=join(root,markerName),s=await fs.lstat(path);
  if(!s.isFile()||s.isSymbolicLink()||s.size>4096||s.uid!==uid())throw Error('Missing or unsafe ownership marker');
  const file=await fs.open(path,constants.O_RDONLY|(constants.O_NOFOLLOW??0)|(constants.O_NONBLOCK??0));
  let marker;
  try{const opened=await file.stat();if(!opened.isFile()||opened.dev!==s.dev||opened.ino!==s.ino||opened.size!==s.size)throw Error('Ownership marker changed');marker=JSON.parse(await file.readFile('utf8'));}finally{await file.close();}
  if(marker.version!==1||marker.purpose!==purpose||marker.root!==root||marker.uid!==uid()||!/^[-a-f0-9]{36}$/.test(marker.nonce))throw Error('Refused a foreign ownership marker');
  marker.destination=await outputPath(marker.destination);return marker;
}
function validatedLimits(changes){
  const limits={...offlineCILimits,...changes};
  for(const [name,value]of Object.entries(limits))if(!(name in offlineCILimits)||!Number.isSafeInteger(value)||value<1||value>offlineCILimits[name])throw Error('Invalid or enlarged collector limit');
  if(limits.manifestBytes<4096||limits.totalBytes<=limits.manifestBytes||limits.fileBytes>limits.totalBytes)throw Error('Collector needs a bounded manifest reserve');
  return limits;
}
async function safeParents(root,name){
  let path=root;for(const part of name.split('/').slice(0,-1)){path=join(path,part);await realDirectory(path);}
}
function profileGroup(name){const p=name.split('/'),i=p.indexOf('private-persistent-profile');return i<0?null:p.slice(0,i+1).join('/');}
function browserMetadata(name){const parts=name.split('/');return !parts.some(p=>forbidden.has(p.toLowerCase())||p.toLowerCase().startsWith('.env'))&&/\.(json|txt|log|zip|png)$/.test(name);}

// failed=false does not inspect source paths or create a destination. The owner
// marker prevents accidental collection of arbitrary checkout/user directories;
// it is not authentication against malicious native code with the same UID.
export async function collectOfflineCIArtifacts({failed=false,ownedRoot,limits:changes={}}={}){
  if(failed!==true)return {status:'skipped-not-failure'};
  const limits=validatedLimits(changes),owner=await ownedMarker(ownedRoot),root=owner.root,out=owner.destination;
  const report={version:1,status:'collected-failure-only',purpose,limits,sourceRoot:root,
    copiedMetadataBytes:0,copiedProfileBytes:0,copiedFiles:0,scannedEntries:0,
    copied:[],omitted:[],profiles:[],recordDetailsTruncated:false};
  const note=(kind,value)=>{const list=report[kind];if(list.length<128)list.push(value);else report.recordDetailsTruncated=true;};
  const metadata=[],groups=new Map();let stopped=false;
  async function candidate(name){
    if(stopped)return;
    if(++report.scannedEntries>limits.scanEntries){stopped=true;note('omitted',{path:name,reason:'scan-entry-limit'});return;}
    if(Buffer.byteLength(name)>limits.pathBytes){note('omitted',{reason:'path-byte-limit'});return;}
    try{await safeParents(root,name);}catch(e){if(e.code==='ENOENT')return;note('omitted',{path:name,reason:'unsafe-parent'});return;}
    const path=join(root,...name.split('/'));let s;
    try{s=await fs.lstat(path);}catch(e){if(e.code==='ENOENT')return;throw e;}
    if(s.isSymbolicLink()){note('omitted',{path:name,reason:'symlink-refused'});return;}
    if(!s.isFile()){note('omitted',{path:name,reason:'nonregular-refused'});return;}
    const item={name,path,bytes:s.size,dev:s.dev,ino:s.ino,mtime:s.mtimeMs};
    const group=profileGroup(name);
    if(group===name){note('omitted',{path:name,reason:'invalid-profile-root'});return;}
    if(group){if(!groups.has(group))groups.set(group,{name:group,files:[],bytes:0,blocked:false});const g=groups.get(group);g.files.push(item);g.bytes+=s.size;if(s.size>limits.fileBytes)g.blocked=true;}
    else if(name.startsWith('browser/')&&!browserMetadata(name))note('omitted',{path:name,reason:'unsupported-browser-diagnostic'});
    else metadata.push(item);
  }
  for(const name of metadataPaths)await candidate(name);
  async function walkBrowser(directory,name,depth){
    if(stopped)return;
    if(depth>limits.depth){stopped=true;note('omitted',{path:name,reason:'depth-limit'});return;}
    let info;try{info=await fs.lstat(directory);}catch(e){if(e.code==='ENOENT')return;throw e;}
    if(info.isSymbolicLink()){note('omitted',{path:name,reason:'symlink-refused'});return;}
    if(!info.isDirectory()){await candidate(name);return;}
    const entries=await fs.opendir(directory);
    for await(const entry of entries){
      if(stopped)break;
      const child=name+'/'+entry.name;
      if(entry.isDirectory()){
        if(++report.scannedEntries>limits.scanEntries){stopped=true;note('omitted',{path:child,reason:'scan-entry-limit'});break;}
        if(!profileGroup(child)&&forbidden.has(entry.name.toLowerCase()))note('omitted',{path:child,reason:'forbidden-subtree'});
        else await walkBrowser(join(directory,entry.name),child,depth+1);
      }else await candidate(child);
    }
  }
  await walkBrowser(join(root,'browser'),'browser',0);
  if(stopped)for(const g of groups.values())g.blocked=true; // never publish a partial profile set as complete
  await fs.mkdir(out,{mode:0o700});
  let payload=0;const inventory=createHash('sha256');
  async function copy(item,role){
    if(report.copiedFiles>=limits.files||item.bytes>limits.fileBytes||payload+item.bytes>limits.totalBytes-limits.manifestBytes){note('omitted',{path:item.name,bytes:item.bytes,reason:'payload-or-file-bound'});return false;}
    await safeParents(root,item.name);
    const source=await fs.open(item.path,constants.O_RDONLY|(constants.O_NOFOLLOW??0)|(constants.O_NONBLOCK??0));
    const target=join(out,...item.name.split('/'));let output,received=0,complete=false;const sha=createHash('sha256');
    try{
      const info=await source.stat();if(!info.isFile()||info.dev!==item.dev||info.ino!==item.ino||info.size!==item.bytes||info.mtimeMs!==item.mtime)throw Error('Failure source changed before copy');
      await fs.mkdir(dirname(target),{recursive:true,mode:0o700});output=await fs.open(target,'wx',0o400);
      const buffer=Buffer.alloc(64<<10);
      for(;;){const read=await source.read(buffer,0,Math.min(buffer.length,item.bytes-received+1),null);if(read.bytesRead===0)break;
        if(received+read.bytesRead>item.bytes)throw Error('Failure source grew during copy');
        const chunk=buffer.subarray(0,read.bytesRead);await output.writeFile(chunk);sha.update(chunk);received+=read.bytesRead;
      }
      const after=await source.stat();if(received!==item.bytes||after.size!==item.bytes||after.mtimeMs!==item.mtime)throw Error('Failure source changed during copy');
      await output.chmod(0o400);await output.close();output=undefined;complete=true;
    }finally{await output?.close();await source.close();if(!complete)try{await fs.unlink(target);}catch(e){if(e.code!=='ENOENT')throw e;}}
    const sum=sha.digest('hex');payload+=received;report.copiedFiles++;report[role==='profile'?'copiedProfileBytes':'copiedMetadataBytes']+=received;
    inventory.update(JSON.stringify([item.name,received,sum])+'\n');note('copied',{path:item.name,bytes:received,sha256:sum,role});return true;
  }
  try{
    // Known manifest/report priority precedes arbitrary browser attachments.
    for(const item of metadata){
      if(item.bytes>limits.fileBytes||report.copiedMetadataBytes+item.bytes>limits.metadataBytes){note('omitted',{path:item.name,bytes:item.bytes,reason:'metadata-or-file-bound'});continue;}
      await copy(item,'metadata');
    }
    for(const g of [...groups.values()].sort((a,b)=>a.name<b.name?-1:1)){
      const reason=g.blocked?'profile-file-or-scan-bound':report.copiedProfileBytes+g.bytes>limits.profileBytes?'profile-byte-bound':payload+g.bytes>limits.totalBytes-limits.manifestBytes?'total-byte-bound':report.copiedFiles+g.files.length>limits.files?'file-count-bound':null;
      if(reason){note('profiles',{path:g.name,bytes:g.bytes,files:g.files.length,status:'omitted',reason});continue;}
      // Whole regular-file group admission happens BEFORE any profile is opened.
      for(const item of g.files)if(!await copy(item,'profile'))throw Error('Profile admission drifted');
      note('profiles',{path:g.name,bytes:g.bytes,files:g.files.length,status:'copied-regular-files-only'});
    }
  }catch(error){report.status='collection-failed';report.error=String(error?.message??error).slice(0,512);}
  report.inventorySHA256=inventory.digest('hex');report.payloadBytes=payload;
  function encoded(){
    let bytes=Buffer.from(JSON.stringify(report,null,2)+'\n');
    for(let i=0;i<5;i++){const n=bytes.length;report.manifestBytes=n;report.totalOutputBytes=payload+n;
      bytes=Buffer.from(JSON.stringify(report,null,2)+'\n');if(bytes.length===n)return bytes;}
    throw Error('Failure manifest size did not stabilize');
  }
  let manifest=encoded();
  while(manifest.length>limits.manifestBytes&&(report.copied.length||report.omitted.length||report.profiles.length)){
    report.recordDetailsTruncated=true;
    const longest=['copied','omitted','profiles'].sort((a,b)=>report[b].length-report[a].length)[0];report[longest].pop();manifest=encoded();
  }
  if(manifest.length>limits.manifestBytes)throw Error('Reserved failure manifest bound exceeded');
  const marker=await fs.open(join(out,'collection.json'),'wx',0o400);try{await marker.writeFile(manifest);await marker.sync();}finally{await marker.close();}
  report.totalOutputBytes=payload+manifest.length;
  if(report.totalOutputBytes>limits.totalBytes)throw Error('Failure output exceeded total bound');
  return report;
}

async function main(args){
  const operation=args.shift(),options={};
  for(let i=0;i<args.length;i++){
    const flag=args[i];if(flag==='--failure'){if(options.failed)throw Error('Duplicate failure flag');options.failed=true;continue;}
    if(!['--root','--out'].includes(flag)||!args[i+1]||options[flag])throw Error('Use init --root ROOT --out NEW_DIR or collect --failure --root ROOT');
    options[flag]=args[++i];
  }
  if(operation==='init'){if(options.failed)throw Error('Failure flag belongs to collection');await initializeOfflineCIArtifacts({ownedRoot:options['--root'],destination:options['--out']});return;}
  if(operation==='collect'){if(options['--out'])throw Error('Collection destination is bound by the ownership marker');const result=await collectOfflineCIArtifacts({failed:options.failed,ownedRoot:options['--root']});process.stdout.write(JSON.stringify({status:result.status,copiedFiles:result.copiedFiles,payloadBytes:result.payloadBytes})+'\n');if(result.status==='collection-failed')process.exitCode=1;return;}
  throw Error('Use init --root ROOT --out NEW_DIR or collect --failure --root ROOT');
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))main(process.argv.slice(2)).catch(error=>{console.error(String(error));process.exitCode=1;});
