import {createServer, request as httpRequest} from 'node:http';
import {spawn} from 'node:child_process';
import {readFile, lstat} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {dirname, resolve} from 'node:path';

export const offlineOrigin = 'http://127.0.0.1:4250';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
async function regular(path, maximum) {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || info.size > maximum || info.size < 1) throw new Error(`Invalid bounded artifact: ${path}`);
  const bytes = await readFile(path);
  if (bytes.length !== info.size || bytes.length > maximum) throw new Error(`Artifact changed while reading: ${path}`);
  return bytes;
}
export async function readOfflineArtifacts(path = process.env.NEXTGO_OFFLINE_ARTIFACTS) {
  if (!path) throw new Error('NEXTGO_OFFLINE_ARTIFACTS must identify two pinned, prebuilt production offline artifacts; this fixture never builds applications.');
  const index = JSON.parse(await regular(path, 64 << 10));
  if (index.version !== 1 || index.origin !== offlineOrigin) throw new Error('Offline artifact index must be version 1 with the exact fixture public origin.');
  const artifacts = {};
  for (const key of ['A', 'B']) {
    const entry = index[key];
    if (!entry || typeof entry.binary !== 'string' || !/^[a-f0-9]{64}$/.test(entry.binarySHA256)) throw new Error(`Missing ${key} binary pin`);
    const binary = resolve(dirname(path), entry.binary), actual = digest(await regular(binary, 256 << 20));
    if (actual !== entry.binarySHA256) throw new Error(`Offline ${key} binary does not match its pin`);
    artifacts[key] = {binary, binarySHA256: actual};
  }
  if (artifacts.A.binarySHA256 === artifacts.B.binarySHA256) throw new Error('Update proof requires two distinct actual production artifacts.');
  return {version: 1, origin: offlineOrigin, ...artifacts};
}

// The fixture controls only availability and which genuine native server is
// selected. It never constructs catalog, worker, HTML or successful app replies.
export async function startOfflineFixture(artifacts) {
  let child, gateway, selected = 'A', closing;
  const records = [], logs = [], processes = [];
  const record = value => {if (records.length === 256) throw new Error('Offline fixture request record bound exceeded'); records.push(value);};
  const log = value => {logs.push(String(value)); while (logs.join('').length > (128 << 10)) logs.shift();};
  async function stopNative() {
    const owned = child; child = undefined;
    if (!owned || owned.pid === undefined) return;
    if (owned.exitCode !== null || owned.signalCode !== null) return;
    const done = new Promise(resolve => owned.once('exit', resolve));
    owned.kill('SIGTERM');
    let timer;
    try {await Promise.race([done, new Promise(resolve => {timer = setTimeout(() => {owned.kill('SIGKILL'); resolve();}, 2500);})]); await done;} finally {clearTimeout(timer);}
  }
  async function startNative(key) {
    if (child) throw new Error('Native server already running');
    selected = key;
    const port = key === 'A' ? 4251 : 4252;
    const owned = spawn(artifacts[key].binary, ['-addr', `127.0.0.1:${port}`], {
      cwd: dirname(artifacts[key].binary), stdio: ['ignore', 'pipe', 'pipe'],
      env: {...process.env, GOMAXPROCS: '1', NEXTGO_OFFLINE_ORIGIN: offlineOrigin, NEXTGO_OFFLINE_BASE_PATH: ''},
    });
    child = owned;
    const processProof = {artifact: key, pid: owned.pid ?? null, exited: false};
    processes.push(processProof);
    owned.once('exit', (code, signal) => Object.assign(processProof, {exited: true, code, signal}));
    owned.stdout.on('data', log); owned.stderr.on('data', log);
    let failure; owned.once('error', error => {failure = error;});
    for (let n = 0; n < 150; n++) {
      if (failure || owned.exitCode !== null || owned.signalCode !== null) throw failure || new Error(`Native ${key} exited before readiness: ${logs.join('')}`);
      try {const response = await fetch(`http://127.0.0.1:${port}/api/health`, {headers: {Host: '127.0.0.1:4250'}, signal: AbortSignal.timeout(500)}); await response.arrayBuffer(); if (response.status === 200) return;} catch {}
      await pause(100);
    }
    throw new Error(`Native ${key} readiness timed out`);
  }
  async function startGateway() {
    if (gateway) throw new Error('Gateway already running');
    const owned = createServer((req, res) => {
      if (req.headers.host !== '127.0.0.1:4250' || typeof req.url !== 'string' || !req.url.startsWith('/') || req.url.startsWith('//') || req.url.includes('\\')) {res.writeHead(400).end(); return;}
      if (!child) {req.socket.destroy(); return;}
      const entry = {generation: selected, method: req.method, path: req.url, status: null};
      try {record(entry);} catch {res.writeHead(503).end(); return;}
      let received = 0;
      const upstream = httpRequest({hostname: '127.0.0.1', port: selected === 'A' ? 4251 : 4252, path: req.url, method: req.method, headers: req.headers}, actual => {
        entry.status = actual.statusCode;
        res.writeHead(actual.statusCode, actual.headers); actual.pipe(res);
        actual.on('error', () => res.destroy());
      });
      req.on('data', chunk => {received += chunk.length; if (received > (64 << 10)) {upstream.destroy(); req.destroy();}});
      req.on('aborted', () => upstream.destroy());
      res.on('close', () => {if (!res.writableFinished) upstream.destroy();});
      upstream.on('error', () => res.destroy()); req.pipe(upstream);
    });
    owned.headersTimeout = 10_000; owned.requestTimeout = 20_000;
    await new Promise((resolve, reject) => {owned.once('error', reject); owned.listen(4250, '127.0.0.1', resolve);});
    gateway = owned;
  }
  async function stopGateway() {
    const owned = gateway; gateway = undefined; if (!owned) return;
    await new Promise(resolve => {owned.close(resolve); owned.closeAllConnections();});
  }
  const fixture = {
    origin: offlineOrigin, artifacts, records, logs,
    async stopNetwork() {await stopGateway(); await stopNative();},
    async startNetwork(key = selected) {await startNative(key); await startGateway();},
    async select(key) {if (!['A', 'B'].includes(key)) throw new Error('Unknown artifact'); await stopNative(); await startNative(key);},
    snapshot() {return {artifacts, selected, nativePID: child?.pid ?? null, records: records.map(r => ({...r})), processes: processes.map(p => ({...p})), gatewayListening: Boolean(gateway?.listening), logs: logs.join('')};},
    async close() {closing ||= (async () => {await stopGateway(); await stopNative(); for (const key of ['A','B']) {const bytes = await regular(artifacts[key].binary, 256 << 20); if (digest(bytes) !== artifacts[key].binarySHA256) throw new Error('Artifact changed during qualification');}})(); return closing;},
  };
  try {await fixture.startNetwork('A'); return fixture;} catch (error) {await fixture.close(); throw error;}
}
