// CI/dev-only artifact producer and bounded owner. It is never runtime code.
import {spawn} from 'node:child_process';
import {constants} from 'node:fs';
import {chmod, lstat, mkdir, mkdtemp, open, opendir, readdir, realpath, rm, writeFile} from 'node:fs/promises';
import {basename, delimiter, dirname, isAbsolute, join, relative, sep} from 'node:path';
import {pathToFileURL} from 'node:url';
import {createHash} from 'node:crypto';

export const check = (ok, message) => {if (!ok) throw Error(message);};
export const sha = bytes => createHash('sha256').update(bytes).digest('hex');
export const variants = ['EAGER_ROOT','EAGER_MOUNTED','LAZY_ROOT','LAZY_MOUNTED'];
const generated = ['routes/nextgo_gen.go','actionrefs/nextgo_gen.go','actionrefs/nextgo_gen_js.go','actionrefs/nextgo_gen_server.go'].map(path => 'examples/static-shell/' + path);
const testerPins = {'tests/static-shell-host.mjs':'a5437350bd6da54ac52a0fadf311505deffc32daf348b2a42db46c75f8921513','tests/static-shell.spec.mjs':'7a912c372a7589db4329a3379aca7443057bfdecd4bd8a22480a6f4595127c61','tests/static-shell.config.mjs':'fa17f9935fe04125d8b4d1cbb3b37e4684d21840fa156a1b289a4502c9c16078'};
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const outside = (parent, path) => {const rel = relative(parent, path); return isAbsolute(rel) || rel === '..' || rel.startsWith('..' + sep);};

export async function readBounded(path, maximum = 16 << 20) {
  check(isAbsolute(path) && await realpath(path) === path,'file or parent alias rejected: '+path);
  const before = await lstat(path);
  check(before.isFile() && !before.isSymbolicLink() && before.nlink === 1 && before.size <= maximum, 'unsafe or oversized file: ' + path);
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const opened = await file.stat();
    check(opened.isFile() && opened.ino === before.ino && opened.dev === before.dev && opened.size === before.size, 'file changed before read');
    const buffer = Buffer.allocUnsafe(before.size + 1);
    let used = 0;
    while (used < buffer.length) {const read = await file.read(buffer, used, buffer.length - used, null); if (!read.bytesRead) break; used += read.bytesRead;}
    const after = await file.stat();
    check(used === before.size && after.size === before.size && after.mtimeMs === before.mtimeMs, 'file changed during bounded read');
    return buffer.subarray(0, used);
  } finally {await file.close();}
}
export async function writeJSON(path, value, maximum = 8 << 20) {
  const bytes = Buffer.from(JSON.stringify(value, null, 2) + '\n');
  check(bytes.length <= maximum, 'JSON output limit');
  await writeFile(path, bytes, {mode:0o600});
}
export async function json(path, maximum) {return JSON.parse((await readBounded(path, maximum)).toString('utf8'));}

export async function ownedRoot() {
  const root = process.env.NEXTGO_STATIC_SHELL_CI;
  check(isAbsolute(root || '') && await realpath(root) === root && basename(root).startsWith('nextgo-static-shell-ci.'), 'explicit owned CI root required');
  const source = await realpath(process.env.GITHUB_WORKSPACE || process.cwd());
  const temp = await realpath(process.env.RUNNER_TEMP || '');
  const info = await lstat(root);
  check(info.isDirectory() && !info.isSymbolicLink() && info.uid === process.getuid() && !(info.mode & 0o077) && outside(source,root) && !outside(temp,root), 'foreign CI root rejected');
  const marker = await json(join(root,'owner.json'), 4096);
  check(marker.purpose === 'static-shell-ci-v1' && marker.root === root && marker.source === source && marker.run === process.env.GITHUB_RUN_ID && marker.job === process.env.GITHUB_JOB, 'CI ownership marker mismatch');
  return {root, source};
}

export async function inventory(root, source = false) {
  const records = []; let total = 0;
  const ignored = new Set(['.git','node_modules','.nextgo','.nextgo-test-results','dist','test-results','test-results-crossbrowser','playwright-report']);
  async function walk(directory, depth) {
    check(depth <= 24, 'inventory depth exceeded');
    for (const entry of (await readdir(directory,{withFileTypes:true})).sort((a,b) => a.name.localeCompare(b.name,'en'))) {
      if (source && ignored.has(entry.name)) continue;
      const path = join(directory,entry.name), info = await lstat(path);
      check(!info.isSymbolicLink(), 'inventory symlink rejected: ' + path);
      if (info.isDirectory()) {await walk(path,depth+1); continue;}
      check(records.length < 8192, 'inventory file count exceeded');
      const bytes = await readBounded(path, source ? 16 << 20 : 128 << 20);
      total += bytes.length; check(total <= (source ? 128 << 20 : 512 << 20), 'inventory byte bound exceeded');
      records.push({path:relative(root,path).split(sep).join('/'),bytes:bytes.length,sha256:sha(bytes),mode:info.mode & 0o777,mtimeMs:info.mtimeMs,ctimeMs:info.ctimeMs});
    }
  }
  await walk(root,0);
  return records;
}
const content = entries => entries.map(({path,bytes,sha256}) => ({path,bytes,sha256}));
const unchanged = (before,after,allowed = []) => check(JSON.stringify(before.filter(item => !allowed.includes(item.path))) === JSON.stringify(after.filter(item => !allowed.includes(item.path))), 'unexpected source mutation');

const groupExists = pid => {try {process.kill(-pid,0); return true;} catch (error) {if (error.code === 'ESRCH') return false; throw error;}};
export function commandEnvironment(root, extras = {}) {
  const environment = {};
  for (const key of ['PATH','HOME','USER','LOGNAME','LANG','LC_ALL','GOROOT']) if (process.env[key]) environment[key] = process.env[key];
  return {...environment, CI:'true', GITHUB_ACTIONS:'true', GOTOOLCHAIN:'local', GOENV:'off', GOWORK:'off', GOFLAGS:'-p=1 -buildvcs=false', GOMAXPROCS:'1', GOMEMLIMIT:'384MiB', GOTELEMETRY:'off', GOPROXY:'off', GOSUMDB:'off', GOCACHE:join(root,'go-cache'), GOMODCACHE:join(root,'mod-cache'), TMPDIR:join(root,'tmp'), PLAYWRIGHT_BROWSERS_PATH:join(root,'browsers'), ...extras};
}

// Every subprocess gets its own Linux process group. A successful close is
// accepted only after actual stdio close and ESRCH for the owned group.
export async function runChild(root, name, command, args, {cwd,env,timeoutMS,stdoutLimit = 1 << 20} = {}) {
  check(/^[a-z0-9-]+$/.test(name) && Number.isInteger(timeoutMS) && timeoutMS > 0, 'invalid finite child command');
  const proof = {name,command,args,pid:null,code:null,signal:null,closed:false,groupEmpty:false,unexpectedDescendants:false,stdoutBytes:0,stderrBytes:0,outputExceeded:false,timedOut:false,interrupted:false,cleanupTimedOut:false,termSent:false,killSent:false};
  const buffers = {stdout:[],stderr:[]};
  let child, watchdog, closeResolve, stopResolve, spawnError;
  const closed = new Promise(resolve => {closeResolve = resolve;});
  const stopped = new Promise(resolve => {stopResolve = resolve;});
  const signalGroup = signal => {try {process.kill(-child.pid,signal);} catch (error) {if (error.code !== 'ESRCH') throw error;}};
  let stopping;
  const stop = () => stopping ||= (async () => {
    const deadline = Date.now() + 5000;
    try {
      proof.termSent = true; signalGroup('SIGTERM');
      while (Date.now() < deadline && (!proof.closed || groupExists(child.pid))) {
        if (!proof.killSent && Date.now() >= deadline - 2000) {proof.killSent = true; signalGroup('SIGKILL');}
        await pause(Math.min(40, Math.max(1,deadline-Date.now())));
      }
      proof.groupEmpty = !groupExists(child.pid);
      proof.cleanupTimedOut = !proof.closed || !proof.groupEmpty;
    } catch (error) {proof.cleanupTimedOut = true; proof.cleanupError = String(error.message).slice(0,160);}
    finally {stopResolve();}
  })();
  const interrupt = () => {proof.interrupted = true; if (child?.pid) void stop();};
  process.on('SIGINT',interrupt); process.on('SIGTERM',interrupt);
  try {
    child = spawn(command,args,{cwd,env,detached:true,stdio:['ignore','pipe','pipe']});
    proof.pid = child.pid ?? null;
    child.once('error', error => {spawnError = error;});
    child.once('close',(code,signal) => {Object.assign(proof,{code,signal,closed:true}); closeResolve();});
    for (const stream of ['stdout','stderr']) child[stream].on('data', chunk => {
      const key = stream + 'Bytes', cap = stream === 'stdout' ? stdoutLimit : 1 << 20;
      const room = Math.max(0,cap-proof[key]);
      if (room) buffers[stream].push(chunk.subarray(0,room));
      proof[key] += chunk.length;
      if (proof[key] > cap) {proof.outputExceeded = true; void stop();}
    });
    watchdog = setTimeout(() => {proof.timedOut = true; if (child.pid) void stop();},timeoutMS);
    await Promise.race([closed,stopped]);
    proof.unexpectedDescendants = proof.closed && child.pid ? groupExists(child.pid) : false;
    if (child.pid && (stopping || proof.unexpectedDescendants)) await stop();
    proof.groupEmpty = child.pid ? !groupExists(child.pid) : true;
    if (stopping) await stopping;
    check(!spawnError && proof.closed && proof.groupEmpty && !proof.unexpectedDescendants && !proof.cleanupTimedOut && !proof.timedOut && !proof.interrupted && !proof.outputExceeded && proof.code === 0 && proof.signal === null, 'owned command failed: ' + name);
    return Buffer.concat(buffers.stdout);
  } finally {
    clearTimeout(watchdog); process.off('SIGINT',interrupt); process.off('SIGTERM',interrupt);
    await writeFile(join(root,'logs',name+'.stdout'),Buffer.concat(buffers.stdout),{mode:0o600});
    await writeFile(join(root,'logs',name+'.stderr'),Buffer.concat(buffers.stderr),{mode:0o600});
    await writeJSON(join(root,'logs',name+'.proof.json'),proof,64 << 10);
  }
}

async function init() {
  check(process.platform === 'linux' && process.arch === 'x64' && process.version === 'v24.22.1', 'CI requires Linux AMD64 Node24.22.1');
  const source = await realpath(process.env.GITHUB_WORKSPACE || process.cwd());
  const root = await mkdtemp(join(await realpath(process.env.RUNNER_TEMP),'nextgo-static-shell-ci.'));
  await chmod(root,0o700);
  for (const directory of ['logs','tmp','go-cache','mod-cache','npm-cache','browsers','exports','generated','report','browser','failure']) await mkdir(join(root,directory),{mode:0o700});
  await writeJSON(join(root,'owner.json'),{purpose:'static-shell-ci-v1',root,source,run:process.env.GITHUB_RUN_ID,job:process.env.GITHUB_JOB},4096);
  check(outside(source,root),'CI root must be outside checkout');
  const values = {NEXTGO_STATIC_SHELL_CI:root,GOCACHE:join(root,'go-cache'),GOMODCACHE:join(root,'mod-cache'),npm_config_cache:join(root,'npm-cache'),TMPDIR:join(root,'tmp'),PLAYWRIGHT_BROWSERS_PATH:join(root,'browsers'),NEXTGO_E2E_RESULTS:join(root,'browser'),PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','results.json')};
  check(typeof process.env.GITHUB_ENV === 'string','GitHub environment output required');
  await writeFile(process.env.GITHUB_ENV,Object.entries(values).map(([key,value]) => key+'='+value+'\n').join(''),{flag:'a'});
}

async function selectedSDK() {
  let go;
  for (const directory of process.env.PATH.split(delimiter)) {
    try {const candidate = await realpath(join(directory,'go')); if ((await lstat(candidate)).isFile()) {go = candidate; break;}} catch {}
  }
  check(go,'selected Go binary missing from PATH');
  const root = dirname(dirname(go)), versionBytes = await readBounded(join(root,'VERSION'),4096);
  const version = versionBytes.toString('utf8').split('\n')[0];
  check(version === 'go'+process.env.GO_VERSION && ['go1.24.13','go1.27.3'].includes(version),'selected SDK VERSION mismatch');
  if (process.env.GOROOT) check(await realpath(process.env.GOROOT) === root,'GOROOT/PATH compiler mismatch');
  const files = {};
  for (const path of ['bin/go','pkg/tool/linux_amd64/compile','pkg/tool/linux_amd64/link','lib/wasm/wasm_exec.js']) {const bytes = await readBounded(join(root,path),128 << 20); files[path] = {bytes:bytes.length,sha256:sha(bytes)};}
  return {root,go,version,versionFile:versionBytes.toString('utf8'),files};
}

async function build() {
  check(process.platform === 'linux' && process.arch === 'x64' && process.version === 'v24.22.1','producer requires Linux AMD64 Node24.22.1');
  const {root,source} = await ownedRoot();
  const report = {version:1,scope:'Linux AMD64 functional CI only',status:'BUILDING',commit:process.env.GITHUB_SHA,run:process.env.GITHUB_RUN_ID,platform:process.platform,architecture:process.arch,node:process.version,sdk:await selectedSDK(),steps:[],artifacts:{},generated:[]};
  await writeJSON(join(root,'build.json'),report);
  const before = await inventory(source,true);
  for (const [path,pin] of Object.entries(testerPins)) check(sha(await readBounded(join(source,path))) === pin,'reviewed frozen tester changed: '+path);
  const pw = await json(join(source,'node_modules','@playwright','test','package.json'),64 << 10);
  check(pw.version === '1.64.1','locked Playwright version mismatch'); report.playwright = pw.version;
  const cli = join(root,'nextgo'), app = './examples/static-shell', env = commandEnvironment(root,{GOROOT:report.sdk.root});
  async function step(name,command,args,extra = {},timeoutMS = 7*60000) {
    report.steps.push({name,command,args,extra}); await writeJSON(join(root,'build.json'),report);
    await runChild(root,name,command,args,{cwd:source,env:{...env,...extra},timeoutMS});
  }
  try {
    await step('build-cli',report.sdk.go,['build','-trimpath','-buildvcs=false','-o',cli,'./cmd/nextgo'],{},5*60000);
    unchanged(before,await inventory(source,true));
    await step('generate',cli,['generate',app],{},60000);
    const afterGenerate = await inventory(source,true); unchanged(before,afterGenerate,generated);
    for (const path of generated) {
      const bytes = await readBounded(join(source,path),1 << 20);
      check(bytes.toString('utf8').startsWith('// Code generated by nextgo'),'actual generated helper header missing');
      const target = join(root,'generated',basename(dirname(path))+'-'+basename(path));
      await writeFile(target,bytes,{flag:'wx',mode:0o400}); report.generated.push({path,bytes:bytes.length,sha256:sha(bytes),captured:relative(root,target)});
    }
    await step('generate-check',cli,['generate','--check',app],{},60000);
    unchanged(afterGenerate,await inventory(source,true)); report.checkModeNoWrite = true;
    const {validateStaticArtifact} = await import('./static-shell-host.mjs');
    for (const variant of variants) {
      const output = join(root,'exports',variant.toLowerCase());
      const extra = {NEXTGO_STATIC_SHELL_MODE:variant.startsWith('LAZY_')?'lazy':'eager',NEXTGO_STATIC_SHELL_BASE:variant.endsWith('_MOUNTED')?'/mounted':''};
      await step('export-'+variant.toLowerCase().replaceAll('_','-'),cli,['export','-out',output,app],extra);
      unchanged(afterGenerate,await inventory(source,true));
      const manifestSHA256 = sha(await readBounded(join(output,'.nextgo-export.json')));
      const validated = await validateStaticArtifact({variant,root:output,manifestSHA256});
      const files = await inventory(output);
      const shim = files.find(entry => entry.path.endsWith('/wasm_exec.js'));
      check(shim && shim.sha256 === report.sdk.files['lib/wasm/wasm_exec.js'].sha256,'compiler/runtime wasm_exec mismatch');
      for (const file of files) await chmod(join(output,file.path),0o444);
      async function lock(directory) {for (const entry of await readdir(directory,{withFileTypes:true})) if (entry.isDirectory()) await lock(join(directory,entry.name)); await chmod(directory,0o555);}
      await lock(output);
      report.artifacts[variant] = {root:output,manifestSHA256,hostInventorySHA256:validated.inventorySHA256,build:validated.manifest.build,configuration:validated.manifest.configuration,descriptor:validated.descriptor,files:content(files)};
      await writeJSON(join(root,'build.json'),report);
    }
    check(report.steps.length === 7 && report.generated.length === 4,'seven real producer steps/four helpers required');
    report.sourceFiles = afterGenerate; report.sourceSHA256 = sha(Buffer.from(JSON.stringify(content(afterGenerate))));
    report.cliSHA256 = sha(await readBounded(cli,128 << 20)); report.status = 'BUILT';
  } catch (error) {report.status = 'FAILED'; report.failure = String(error.message).slice(0,2048); throw error;}
  finally {await writeJSON(join(root,'build.json'),report);}
}

async function collect() {
  const {root} = await ownedRoot();
  const totalLimit = 64 << 20, manifestLimit = 64 << 10, payloadLimit = totalLimit-manifestLimit;
  const copied = []; let bytes = 0;
  async function copy(path, name, cap) {
    try {const data = await readBounded(path,cap); check(bytes+data.length <= payloadLimit,'evidence collection total cap'); await writeFile(join(root,'failure',name),data,{mode:0o600}); bytes += data.length; copied.push({name,bytes:data.length,sha256:sha(data)});} catch (error) {copied.push({name,omitted:String(error.code || error.message).slice(0,160)});}
  }
  for (const name of ['build.json','browser-proof.json']) await copy(join(root,name),name,8 << 20);
  for (const name of ['discovery.json','results.json']) await copy(join(root,'report',name),name,16 << 20);
  for (const entry of (await readdir(join(root,'logs'))).slice(0,64)) await copy(join(root,'logs',entry),'log-'+entry,1 << 20);
  const attachments = []; let visitedEntries=0, visitedDirectories=0;
  async function walk(directory,depth) {
    if (depth>4 || attachments.length>=16 || visitedEntries>=512 || visitedDirectories>=128) return;
    visitedDirectories++;
    const handle=await opendir(directory);
    try {
      while (attachments.length<16 && visitedEntries<512) {
        const entry=await handle.read(); if(!entry) break; visitedEntries++;
        const path=join(directory,entry.name); if(entry.isSymbolicLink()) continue;
        if(entry.isDirectory()) await walk(path,depth+1);
        else if(entry.isFile() && /(?:\.zip|\.png|\.json)$/.test(entry.name)) attachments.push(path);
      }
    } finally {await handle.close();}
  }
  try {await walk(join(root,'browser'),0);} catch(error) {copied.push({name:'browser-traversal',omitted:String(error.code || error.message).slice(0,160)});}
  for (let index=0;index<attachments.length;index++) await copy(attachments[index],'browser-'+index+'-'+basename(attachments[index]),8 << 20);
  for (const entry of (await readdir(join(root,'generated'))).slice(0,4)) await copy(join(root,'generated',entry),'generated-'+entry,1 << 20);
  await writeJSON(join(root,'failure','collection.json'),{version:1,payloadBytes:bytes,payloadLimit,manifestLimit,totalLimit,visitedEntries,visitedDirectories,copied},manifestLimit);
}
async function cleanup() {
  const {root} = await ownedRoot();
  // Only this marker-verified RUNNER_TEMP subtree is removed. The runner still
  // owns hard process termination if any supervised group could not join.
  for (const name of (await readdir(join(root,'logs'))).filter(name=>name.endsWith('.proof.json')).slice(0,64)) {
    const proof = await json(join(root,'logs',name),64 << 10);
    check(!Number.isSafeInteger(proof.pid) || !groupExists(proof.pid),'owned process group still exists; outer job controller must terminate it before filesystem cleanup');
  }
  async function unlock(directory) {await chmod(directory,0o700); for (const entry of await readdir(directory,{withFileTypes:true})) if(entry.isDirectory() && !entry.isSymbolicLink()) await unlock(join(directory,entry.name));}
  await unlock(root);
  await rm(root,{recursive:true,force:false,maxRetries:0});
  try {await lstat(root); throw Error('owned CI root still exists');} catch (error) {if (error.code !== 'ENOENT') throw error;}
  process.stdout.write('Marker-verified owned CI root is absent.\n');
}

async function main() {
  const mode = process.argv[2];
  if (mode === 'init') return init();
  if (mode === 'build') return build();
  if (mode === 'collect') return collect();
  if (mode === 'cleanup') return cleanup();
  if (mode === 'install') {const {root,source}=await ownedRoot(); const env={...process.env,PLAYWRIGHT_BROWSERS_PATH:join(root,'browsers')}; delete env.PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD; return runChild(root,'install-browsers',process.execPath,[join(source,'node_modules','playwright','cli.js'),'install','--with-deps','chromium','firefox','webkit'],{cwd:source,env,timeoutMS:59*60000});}
  throw Error('expected init/build/install/collect/cleanup');
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(error => {console.error(String(error.stack || error)); process.exitCode=1;});
