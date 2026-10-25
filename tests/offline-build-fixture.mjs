// Build two real offline artifacts using an explicitly supplied, already built CLI.
// This helper never builds the CLI, starts a server/browser, or deletes user data.
import {spawn, execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createHash, randomUUID} from 'node:crypto';
import {constants} from 'node:fs';
import * as fs from 'node:fs/promises';
import {finished} from 'node:stream/promises';
import {createWriteStream} from 'node:fs';
import {dirname, basename, resolve, relative, join, sep, isAbsolute} from 'node:path';
import {fileURLToPath} from 'node:url';

export const offlineBuildOrigin = 'http://127.0.0.1:4250';
export const offlineGuideA = 'Read the saved edition deliberately.';
export const offlineGuideB = 'Read the updated saved edition deliberately.';
const guidePath = 'examples/offline/app/guide/page.go';
const frameworkModule = 'github.com/Suhaibinator/Next.go';
const defaultSource = fileURLToPath(new URL('../', import.meta.url));
const limits = Object.freeze({fileBytes:32 << 20, sourceBytes:128 << 20, files:16384,
  gitBytes:8 << 20, manifestBytes:16 << 20, artifactBytes:256 << 20, logBytes:4 << 20,
  commandMS:10 * 60_000, generateMS:60_000});
const execFileAsync = promisify(execFile);
const hash = value => createHash('sha256').update(value).digest('hex');
const utf8 = bytes => new TextDecoder('utf-8', {fatal:true}).decode(bytes);
const within = (parent, child) => {const name=relative(parent,child);return name===''||(!name.startsWith('..'+sep)&&name!=='..'&&!isAbsolute(name));};
const privateDirectoryNames = new Set(['.git','.aws','.ssh','.codex','.agents','.nextgo','.nextgo-test-results',
  'node_modules','dist','test-results','test-results-crossbrowser','playwright-report','__pycache__']);

export function offlineBuildHelp() {
  return `Usage: NEXTGO_TEST_NEXTGO_BINARY=/absolute/nextgo GOCACHE=/existing/cache node tests/offline-build-fixture.mjs --out /fresh/private/output [--source /git/module/root]

Run after the framework/example are frozen, generated refs are current, and all required new files are staged.
The existing CLI must implement this checkout's offline compiler. Optional NEXTGO_TEST_NEXTGO_BINARY_SHA256 pins it.
No CLI/SDK bootstrap, downloads, server, browser, cloud operation, or overwrite is performed.

The helper snapshots tracked/STAGED working-tree files only, rejects unsafe file types/paths, and creates read-only
input files in fresh A/B module copies. B changes exactly one known guide heading literal. It runs generate -check
and production build with literal argument arrays, GOMAXPROCS=1, GOFLAGS='-p=1 -mod=readonly', GOTOOLCHAIN=local,
GOENV/GOWORK/GOPROXY/GOSUMDB=off and exact fixture origin ${offlineBuildOrigin}. GOCACHE must already exist.
GOMODCACHE may identify an existing cache; otherwise an empty owned cache is used with downloads disabled.
Output must be outside every Git worktree; this prevents accidental parent-repository VCS stamping of the copies.

Only after BOTH builds and source checks succeed, index.json is atomically published without replacing anything:
NEXTGO_OFFLINE_ARTIFACTS=/fresh/private/output/index.json node scripts/test-browser.mjs --config tests/offline.config.mjs
Each index entry pins artifacts/{A,B}/server by SHA256. Source manifests, the exact one-literal diff, CLI/SDK identity,
commands, bounded logs and build-report.json are retained. Failed output is preserved with no published index;
choose a NEW output directory for another explicitly approved run. No automatic cleanup or retry occurs.
Limits: 32MiB/source file, 128MiB/source copy, 16384 files, 256MiB/native artifact, 4MiB/command log, 10min/build.
These are fixture payload bounds, not a browser/Go-cache/RSS quota or bit-for-bit reproducibility claim.
`;
}

function sourceName(name) {
  if (!name || name.length>4096 || name.startsWith('/') || name.includes('\\') || /[\x00-\x1f\x7f]/.test(name)) throw Error('Unsafe tracked source path');
  const parts=name.split('/');
  if (parts.some(p=>p===''||p==='.'||p==='..')) throw Error('Aliased tracked source path');
  return name;
}
function excludedName(name) {
  const parts=name.split('/');
  if(parts.some(p=>privateDirectoryNames.has(p.toLowerCase())||p.toLowerCase().startsWith('test-results-')))return 'private/build-output directory';
  if(parts.some(p=>{const lower=p.toLowerCase();return (lower==='.env'||lower.startsWith('.env.'))&&!lower.endsWith('.example');}))return 'private environment file';
  return null;
}
async function directory(path) {
  const info=await fs.lstat(path);
  if(!info.isDirectory()||info.isSymbolicLink())throw Error('Expected a real directory: '+path);
}
async function noSymlinkParents(root, name) {
  let current=root;
  for(const part of name.split('/').slice(0,-1)){current=join(current,part);await directory(current);}
}
async function readPinned(path, maximum, contents=true) {
  const before=await fs.lstat(path);
  if(!before.isFile()||before.isSymbolicLink()||before.size<1||before.size>maximum)throw Error('Expected bounded nonempty regular file: '+path);
  const handle=await fs.open(path,constants.O_RDONLY|(constants.O_NOFOLLOW??0));
  try {
    const opened=await handle.stat();
    if(!opened.isFile()||opened.dev!==before.dev||opened.ino!==before.ino||opened.size!==before.size)throw Error('File changed while opening: '+path);
    const digest=createHash('sha256'),chunks=[];let bytes=0;
    for await (const chunk of handle.createReadStream({autoClose:false})) {
      bytes+=chunk.length;if(bytes>maximum||bytes>before.size)throw Error('File grew while reading: '+path);
      digest.update(chunk);if(contents)chunks.push(chunk);
    }
    const after=await handle.stat();
    if(bytes!==before.size||after.size!==before.size||after.mtimeMs!==opened.mtimeMs)throw Error('File changed while reading: '+path);
    return {bytes,sha256:digest.digest('hex'),mode:before.mode&0o777,contents:contents?Buffer.concat(chunks,bytes):undefined};
  } finally {await handle.close();}
}
async function sourceFile(path, maximum) {
  // Empty ordinary source files are allowed; compiler/public inventory policy is stricter where required.
  const info=await fs.lstat(path);
  if(info.isFile()&&!info.isSymbolicLink()&&info.size===0)return {bytes:0,sha256:hash(Buffer.alloc(0)),mode:info.mode&0o777,contents:Buffer.alloc(0)};
  return readPinned(path,maximum);
}
async function writeJSON(path, value, mode=0o400) {
  const bytes=Buffer.from(JSON.stringify(value,null,2)+'\n');
  if(bytes.length>limits.manifestBytes)throw Error('Fixture metadata exceeds its bound');
  const handle=await fs.open(path,'wx',mode);
  try{await handle.writeFile(bytes);await handle.chmod(mode);await handle.sync();}finally{await handle.close();}
  return hash(bytes);
}
function gitEnvironment() {
  return {PATH:process.env.PATH, HOME:process.env.HOME, LC_ALL:'C', GIT_OPTIONAL_LOCKS:'0',
    GIT_TERMINAL_PROMPT:'0',GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:process.platform==='win32'?'NUL':'/dev/null'};
}
async function git(root,args,maximum=limits.gitBytes) {
  const {stdout}=await execFileAsync('git',['-C',root,...args],{encoding:'buffer',maxBuffer:maximum,timeout:30_000,env:gitEnvironment()});
  return stdout;
}
async function trackedFiles(root) {
  const records=utf8(await git(root,['ls-files','--cached','--stage','-z'])).split('\0');
  if(records.pop()!=='')throw Error('Git source list was not NUL terminated');
  const files=[],excluded=[],seen=new Set();
  for(const record of records) {
    const tab=record.indexOf('\t');if(tab<0)throw Error('Invalid Git source record');
    const [mode,object,stage,...extra]=record.slice(0,tab).split(' ');
    const name=sourceName(record.slice(tab+1));
    if(extra.length||stage!=='0'||!['100644','100755'].includes(mode)||!/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(object))throw Error('Unmerged, nonregular or submodule source is unsupported: '+name);
    if(seen.has(name))throw Error('Duplicate Git source path: '+name);seen.add(name);
    const reason=excludedName(name);if(reason){excluded.push({path:name,reason});continue;}
    files.push({path:name,gitMode:mode,gitObject:object});
  }
  if(files.length===0||files.length>limits.files)throw Error('Tracked source count exceeds the fixture bound');
  files.sort((a,b)=>Buffer.compare(Buffer.from(a.path),Buffer.from(b.path)));
  for(const name of ['go.mod',guidePath,'examples/offline/app/offline.go','offline/build.go','offline/worker/bridge.go','internal/project/offline_build.go']) {
    if(!files.some(file=>file.path===name))throw Error('Required source is not tracked/staged: '+name+'; freeze, generate and stage the offline implementation first');
  }
  return {files,excluded};
}
async function copyA(root,copy,list) {
  await fs.mkdir(copy,{mode:0o700});let total=0;const files=[];
  for(const record of list) {
    await noSymlinkParents(root,record.path);
    const source=resolve(root,record.path);if(!within(root,source))throw Error('Source escaped the module');
    const data=await sourceFile(source,limits.fileBytes);total+=data.bytes;
    if(total>limits.sourceBytes)throw Error('Tracked source exceeds 128 MiB');
    const destination=join(copy,record.path);await fs.mkdir(dirname(destination),{recursive:true,mode:0o700});
    const mode=data.mode&0o111?0o555:0o444;
    await fs.writeFile(destination,data.contents,{flag:'wx',mode});await fs.chmod(destination,mode);
    files.push({...record,bytes:data.bytes,sha256:data.sha256,sourceMode:data.mode,copyMode:mode});
  }
  return {version:1,totalBytes:total,files};
}
async function copyB(a,b,manifest) {
  await fs.mkdir(b,{mode:0o700});const files=[];let patch,total=0;
  for(const record of manifest.files) {
    const data=await sourceFile(join(a,record.path),limits.fileBytes);
    if(data.sha256!==record.sha256||data.bytes!==record.bytes)throw Error('A changed before B snapshot: '+record.path);
    let body=data.contents;
    if(record.path===guidePath) {
      const text=utf8(body),before=JSON.stringify(offlineGuideA),after=JSON.stringify(offlineGuideB);
      if(!Buffer.from(text).equals(body)||text.split(before).length!==2||!text.includes(`ui.El("h1", nil, ui.Text(${before}))`))throw Error('Known UTF-8 guide h1 literal is not unique/current; review the fixture edit before building');
      const lineNumber=text.slice(0,text.indexOf(before)).split('\n').length;
      const oldLine=text.split('\n')[lineNumber-1],newLine=oldLine.replace(before,after);
      body=Buffer.from(text.replace(before,after));
      patch={path:guidePath,before:offlineGuideA,after:offlineGuideB,beforeSHA256:record.sha256,afterSHA256:hash(body),lineNumber,
        unifiedDiff:`--- A/${guidePath}\n+++ B/${guidePath}\n@@ -${lineNumber},1 +${lineNumber},1 @@\n-${oldLine}\n+${newLine}\n`};
    }
    total+=body.length;if(total>limits.sourceBytes)throw Error('B source exceeds the source-copy bound');
    const destination=join(b,record.path);await fs.mkdir(dirname(destination),{recursive:true,mode:0o700});
    await fs.writeFile(destination,body,{flag:'wx',mode:record.copyMode});await fs.chmod(destination,record.copyMode);
    files.push({...record,bytes:body.length,sha256:hash(body)});
  }
  if(!patch)throw Error('The declared guide edit was not made');
  const differences=files.filter((file,i)=>file.sha256!==manifest.files[i].sha256||file.bytes!==manifest.files[i].bytes);
  if(differences.length!==1||differences[0].path!==guidePath)throw Error('B must differ in exactly the one declared guide literal');
  return {manifest:{version:1,totalBytes:files.reduce((n,file)=>n+file.bytes,0),files},patch};
}
async function verifyCopy(root,manifest) {
  const expected=new Map(manifest.files.map(file=>[file.path,file]));
  for(const file of manifest.files) {
    await noSymlinkParents(root,file.path);const data=await sourceFile(join(root,file.path),limits.fileBytes);
    if(data.sha256!==file.sha256||data.bytes!==file.bytes||data.mode!==file.copyMode)throw Error('Frozen copied source changed: '+file.path);
  }
  let count=0;
  async function walk(directory,prefix='') {
    for(const entry of await fs.readdir(directory,{withFileTypes:true})) {
      const name=prefix?prefix+'/'+entry.name:entry.name;
      if(name==='examples/offline/.nextgo') {await directoryIsReal(join(root,name));continue;}
      if(++count>limits.files*4)throw Error('Unexpected copied source entry count');
      if(entry.isSymbolicLink())throw Error('New symlink appeared in the frozen source: '+name);
      if(entry.isDirectory())await walk(join(directory,entry.name),name);
      else if(!entry.isFile()||!expected.has(name))throw Error('Unexpected file appeared in frozen source: '+name);
    }
  }
  await walk(root);
}
const directoryIsReal=directory;
async function existingCache(value,name) {
  if(!value||!isAbsolute(value))throw Error(name+' must identify an existing absolute writable cache directory');
  const canonical=await fs.realpath(value);await directory(canonical);await fs.access(canonical,constants.W_OK);return canonical;
}
function buildEnvironment(cache,modCache) {
  const env={PATH:process.env.PATH,HOME:process.env.HOME,LC_ALL:'C',GOCACHE:cache,GOMODCACHE:modCache,
    GOMAXPROCS:'1',GOFLAGS:'-p=1 -mod=readonly',GOTOOLCHAIN:'local',GOENV:'off',GOWORK:'off',GOPROXY:'off',GOSUMDB:'off',
    GOMEMLIMIT:process.env.GOMEMLIMIT||'384MiB',GOTELEMETRY:'off',CGO_ENABLED:'0',
    NEXTGO_OFFLINE_ORIGIN:offlineBuildOrigin,NEXTGO_OFFLINE_BASE_PATH:'',NEXTGO_ENV_DIR:'',AWS_LAMBDA_RUNTIME_API:''};
  for(const name of ['GOROOT','TMPDIR','GOOS','GOARCH'])if(process.env[name])env[name]=process.env[name];
  return env;
}
async function runCLI(binary,args,{cwd,env,log,timeoutMS}) {
  const stream=createWriteStream(log,{flags:'wx',mode:0o600});
  const child=spawn(binary,args,{cwd,env,shell:false,detached:true,stdio:['ignore','pipe','pipe']});
  const state={binary,args,cwd,startedAt:new Date().toISOString(),pid:child.pid,logBytes:0};let failure;
  let kill;
  const signal=sig=>{
    if(child.pid)try{process.kill(-child.pid,sig);}catch(error){if(error.code!=='ESRCH')failure??=error;}
    if(sig==='SIGTERM'&&!kill)kill=setTimeout(()=>signal('SIGKILL'),2500);
  };
  stream.on('error',error=>{failure??=error;signal('SIGTERM');});
  const timeout=setTimeout(()=>{failure??=Error('Trusted CLI deadline exceeded');signal('SIGTERM');},timeoutMS);
  const interrupted=()=>{failure??=Error('Fixture build interrupted');signal('SIGTERM');};
  process.on('SIGINT',interrupted);process.on('SIGTERM',interrupted);
  for(const input of [child.stdout,child.stderr])input.on('data',bytes=>{
    const remaining=Math.max(0,limits.logBytes-state.logBytes);stream.write(bytes.subarray(0,remaining));state.logBytes+=bytes.length;
    if(state.logBytes>limits.logBytes){failure??=Error('Trusted CLI output exceeded 4 MiB');signal('SIGTERM');}
  });
  try {
    const exit=await new Promise((done,reject)=>{child.once('error',reject);child.once('close',(code,signal)=>done({code,signal}));});
    Object.assign(state,exit);if(exit.code!==0)failure??=Error('Trusted CLI exited with '+exit.code);
  } catch(error){failure??=error;}
  finally {clearTimeout(timeout);clearTimeout(kill);process.removeListener('SIGINT',interrupted);process.removeListener('SIGTERM',interrupted);stream.end();await finished(stream);}
  state.finishedAt=new Date().toISOString();if(failure)state.error=String(failure);
  await writeJSON(log+'.json',state);
  if(failure)throw Error(String(failure)+'; see '+log);
  return state;
}
async function atomicIndex(path,value) {
  const temporary=join(dirname(path),'.offline-index-'+randomUUID());
  const digest=await writeJSON(temporary,value,0o444);
  // link is an atomic no-clobber publish; rename could replace a last-good index.
  await fs.link(temporary,path);
  // Publication is complete; a failed removal of our own temp must not turn a
  // valid published index into a reported build failure. No user file is removed.
  try{await fs.unlink(temporary);}catch{}
  return digest;
}

export async function buildOfflineFixture({sourceRoot=defaultSource,out,cli=process.env.NEXTGO_TEST_NEXTGO_BINARY}={}) {
  if(process.platform==='win32')throw Error('This fixture builder requires Linux/macOS POSIX file and process semantics');
  if(!out||!cli||!isAbsolute(cli))throw Error('Provide --out and absolute NEXTGO_TEST_NEXTGO_BINARY; the helper never builds a CLI');
  const source=await fs.realpath(sourceRoot);await directory(source);
  const gitRoot=utf8(await git(source,['rev-parse','--show-toplevel'],64 << 10)).trimEnd();
  if(await fs.realpath(gitRoot)!==source)throw Error('--source must be the whole tracked Git/module root');
  const desired=resolve(out),parent=await fs.realpath(dirname(desired));await directory(parent);
  const output=join(parent,basename(desired));
  if(within(source,output)||within(output,source))throw Error('Fixture output must be outside the live source worktree');
  try {const inside=utf8(await git(parent,['rev-parse','--is-inside-work-tree'],64 << 10)).trim();if(inside==='true')throw Error('Output parent is in a Git worktree; choose a private temporary location');}
  catch(error){if(!String(error.stderr??'').includes('not a git repository'))throw error;}
  const cache=await existingCache(process.env.GOCACHE,'GOCACHE');
  const cliInfo=await readPinned(cli,limits.artifactBytes,false);
  if(!(cliInfo.mode&0o111))throw Error('NEXTGO_TEST_NEXTGO_BINARY is not executable');
  const requiredPin=process.env.NEXTGO_TEST_NEXTGO_BINARY_SHA256;
  if(requiredPin&&(!/^[a-f0-9]{64}$/.test(requiredPin)||requiredPin!==cliInfo.sha256))throw Error('Prebuilt CLI differs from its explicit SHA256 pin');
  const listing=await trackedFiles(source);
  const moduleFile=await sourceFile(join(source,'go.mod'),limits.fileBytes);
  if(!new RegExp('^module '+frameworkModule.replaceAll('.','\\.')+'\\s*$','m').test(utf8(moduleFile.contents)))throw Error('Tracked source is not the expected framework module');
  await fs.mkdir(output,{mode:0o700}); // Existing output/index is never modified.
  const report={version:1,status:'started',startedAt:new Date().toISOString(),sourceRoot:source,output,
    origin:offlineBuildOrigin,cli:{path:cli,sha256:cliInfo.sha256,bytes:cliInfo.bytes},limits,excluded:listing.excluded,
    sourceScope:'Git tracked/staged working-tree bytes only; no .git or untracked data',commands:[]};
  try {
    const modCache=process.env.GOMODCACHE?await existingCache(process.env.GOMODCACHE,'GOMODCACHE'):join(output,'module-cache');
    if(!process.env.GOMODCACHE)await fs.mkdir(modCache,{mode:0o700});
    const env=buildEnvironment(cache,modCache);report.buildEnvironment=env;
    const sources=join(output,'sources');await fs.mkdir(sources,{mode:0o700});
    const a=join(sources,'A'),b=join(sources,'B');
    const aManifest=await copyA(source,a,listing.files),{manifest:bManifest,patch}=await copyB(a,b,aManifest);
    report.sourceManifestA=await writeJSON(join(output,'source-A.json'),aManifest);
    report.sourceManifestB=await writeJSON(join(output,'source-B.json'),bManifest);
    report.sourceDiff=await writeJSON(join(output,'source-diff.json'),{differences:1,patch});
    if(bManifest.totalBytes>limits.sourceBytes)throw Error('B source exceeds the source-copy bound');
    await verifyCopy(a,aManifest);await verifyCopy(b,bManifest);
    const logs=join(output,'logs'),artifacts=join(output,'artifacts');await fs.mkdir(logs,{mode:0o700});await fs.mkdir(artifacts,{mode:0o755});await fs.chmod(artifacts,0o755);
    const sdk=await execFileAsync('go',['env','-json','GOVERSION','GOROOT','GOOS','GOARCH','GOTOOLCHAIN'],{cwd:a,env,encoding:'utf8',maxBuffer:64 << 10,timeout:30_000});
    report.sdk=JSON.parse(sdk.stdout);
    report.sdk.goBinarySHA256=(await readPinned(join(report.sdk.GOROOT,'bin','go'),limits.artifactBytes,false)).sha256;
    report.sdk.wasmShimSHA256=(await readPinned(join(report.sdk.GOROOT,'lib','wasm','wasm_exec.js'),limits.fileBytes,false)).sha256;
    const index={version:1,origin:offlineBuildOrigin};
    for(const [key,copy,manifest]of [['A',a,aManifest],['B',b,bManifest]]) {
      const app=join(copy,'examples','offline');
      report.commands.push(await runCLI(cli,['generate','-check',app],{cwd:copy,env,log:join(logs,key+'-generate.log'),timeoutMS:limits.generateMS}));
      const artifact=join(artifacts,key);
      report.commands.push(await runCLI(cli,['build','-out',artifact,app],{cwd:copy,env,log:join(logs,key+'-build.log'),timeoutMS:limits.commandMS}));
      await verifyCopy(copy,manifest);
      const binary=join(artifact,'server');await noSymlinkParents(output,relative(output,binary).split(sep).join('/'));
      const pin=await readPinned(binary,limits.artifactBytes,false);
      if(!(pin.mode&0o111))throw Error('Built artifact is not executable: '+key);
      await fs.chmod(binary,0o555);await fs.chmod(artifact,0o755);
      index[key]={binary:relative(output,binary).split(sep).join('/'),binarySHA256:pin.sha256};
    }
    if(index.A.binarySHA256===index.B.binarySHA256)throw Error('A and B must be distinct actual production artifacts');
    for(const key of ['A','B'])if((await readPinned(join(output,index[key].binary),limits.artifactBytes,false)).sha256!==index[key].binarySHA256)throw Error('Built artifact changed before publication: '+key);
    // Recheck the live tracked/staged list and bytes after BOTH immutable builds.
    const after=await trackedFiles(source);
    if(JSON.stringify(after)!==JSON.stringify(listing))throw Error('Live tracked/staged source list changed during fixture build');
    for(const record of aManifest.files){await noSymlinkParents(source,record.path);const data=await sourceFile(join(source,record.path),limits.fileBytes);if(data.sha256!==record.sha256||data.bytes!==record.bytes||data.mode!==record.sourceMode)throw Error('Live source changed during fixture build: '+record.path);}
    if((await readPinned(cli,limits.artifactBytes,false)).sha256!==cliInfo.sha256)throw Error('Trusted prebuilt CLI changed during fixture build');
    await verifyCopy(a,aManifest);await verifyCopy(b,bManifest);
    report.index=index;report.sourcePrePostEqual=true;report.onlySourceContentDelta=guidePath;
    report.contextNote='A/B absolute private roots differ and no .git is copied; no claim that every compiler context or output byte differs solely because of the heading.';
    report.indexSHA256=hash(Buffer.from(JSON.stringify(index,null,2)+'\n'));
    report.publication='Index presence is the final atomic commit; this report describes verified artifacts before publication.';
    report.status='artifacts-verified';report.finishedAt=new Date().toISOString();
    await writeJSON(join(output,'build-report.json'),report);
    report.indexSHA256=await atomicIndex(join(output,'index.json'),index);
    return {index:join(output,'index.json'),indexSHA256:report.indexSHA256,A:index.A,B:index.B};
  } catch(error) {
    report.status='failed';report.error=String(error?.stack??error);report.finishedAt=new Date().toISOString();
    try{await writeJSON(join(output,'failure.json'),report);}catch{}
    throw Error('Offline artifact fixture failed; owned output preserved at '+output+'; '+String(error));
  }
}

async function main(args) {
  if(args.length===0||args.includes('--help')||args.includes('-h')){process.stdout.write(offlineBuildHelp());return;}
  const options={},seen=new Set();
  for(let i=0;i<args.length;i++) {
    const name=args[i];if(!['--out','--source'].includes(name)||seen.has(name)||!args[i+1]||args[i+1].startsWith('--'))throw Error('Use --out FRESH_DIR [--source ROOT]; flags must be unique');
    seen.add(name);options[name==='--out'?'out':'sourceRoot']=args[++i];
  }
  const built=await buildOfflineFixture(options);
  process.stdout.write('Published pinned offline A/B index: '+built.index+'\nSHA256: '+built.indexSHA256+'\n');
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))main(process.argv.slice(2)).catch(error=>{console.error(String(error));process.exitCode=1;});
