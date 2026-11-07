// Private prebuilt-only production fixture. The only constructed responses are
// the trusted local JSON DATA SOURCE, never application HTML or stream frames.
import {createServer, request as httpRequest} from 'node:http';
import {spawn} from 'node:child_process';
import {lstat, mkdtemp, open, readFile, rm} from 'node:fs/promises';
import {constants} from 'node:fs';
import {createHash} from 'node:crypto';
import {join, isAbsolute} from 'node:path';
import {tmpdir} from 'node:os';

export const origin = 'http://127.0.0.1:4160';
export const finalData = Object.freeze({version: 1, title: 'Public metadata resolved — Next.go', description: 'Final metadata arrived without replacing this page’s interactive body.', canonical: 'https://example.com/metadata-stream?phase=final', socialTitle: 'Resolved public metadata', socialDescription: 'One completed Go metadata contribution.'});
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const pause = (ms, signal) => new Promise((resolve, reject) => {
  const onAbort = () => {clearTimeout(timer); reject(signal.reason);};
  const timer = setTimeout(() => {signal.removeEventListener('abort', onAbort); resolve();}, ms);
  if (signal.aborted) {clearTimeout(timer); reject(signal.reason);} else signal.addEventListener('abort', onAbort, {once: true});
});
// Additive qualification evidence only: observe the actual tracked child, not a
// separate probe process. Other platforms honestly report unsupported procfs.
async function observeNativeIdentity(child) {
  if (process.platform !== 'linux') return {supported: false, platform: process.platform, reason: 'linux-procfs-only'};
  const pid = child?.pid;
  if (!Number.isSafeInteger(pid) || pid < 1 || child.exitCode !== null || child.signalCode !== null) return {supported: true, observed: false, reason: 'native-not-live'};
  const bounded = async path => {
    const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const bytes = Buffer.alloc(64 * 1024 + 1); let length = 0;
      while (length < bytes.length) {const part = await file.read(bytes, length, bytes.length - length, null); if (!part.bytesRead) break; length += part.bytesRead;}
      if (length > 64 * 1024) throw new Error('procfs-byte-bound');
      return bytes.subarray(0, length).toString('utf8');
    } finally {await file.close();}
  };
  try {
    const before = await bounded(`/proc/${pid}/stat`), status = await bounded(`/proc/${pid}/status`), after = await bounded(`/proc/${pid}/stat`);
    const fields = text => text.slice(text.lastIndexOf(')') + 2).trim().split(/\s+/);
    const first = fields(before), last = fields(after);
    const uids = /^Uid:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)$/m.exec(status);
    const gids = /^Gid:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)$/m.exec(status);
    const actualPID = /^Pid:\s+(\d+)$/m.exec(status), parentPID = /^PPid:\s+(\d+)$/m.exec(status);
    if (['Z', 'X', 'x'].includes(first[0]) || ['Z', 'X', 'x'].includes(last[0]) || first[19] !== last[19] || !/^\d+$/.test(last[19] || '') || !uids || !gids || Number(actualPID?.[1]) !== pid || Number(parentPID?.[1]) !== process.pid || child.exitCode !== null || child.signalCode !== null) throw new Error('native-identity-changed');
    return {supported: true, observed: true, pid, parentPID: Number(parentPID[1]), startTimeTicks: last[19], processGroup: Number(last[2]), uids: uids.slice(1).map(Number), gids: gids.slice(1).map(Number)};
  } catch {return {supported: true, observed: false, reason: 'native-identity-unconfirmed'};}
}

export async function startMetadataStream(options = {}) {
  const startup = options.signal ? AbortSignal.any([options.signal, AbortSignal.timeout(20_000)]) : AbortSignal.timeout(20_000);
  startup.throwIfAborted();
  const binary = process.env.NEXTGO_E2E_SERVER_BINARY, expected = process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256;
  if (!isAbsolute(binary || '') || !/^[a-f0-9]{64}$/.test(expected || '')) throw new Error('Deferred metadata requires an absolute prebuilt production binary and SHA256; no build fallback exists');
  const info = await lstat(binary);
  if (!info.isFile() || !(info.mode & 0o111) || info.size < 1 || info.size > 128 << 20) throw new Error('Invalid bounded native artifact');
  const bytes = await readFile(binary, {signal: startup});
  if (bytes.length !== info.size || hash(bytes) !== expected) throw new Error('Native artifact pin mismatch');
  const cwd = await mkdtemp(join(tmpdir(), 'nextgo-metadata-stream-native-'));
  const source = [], queue = [], wire = [], sockets = new Set(), upstreams = new Set(), logs = [], listeners = new Map();
  let native, gateway, dataServer, closePromise, httpJoined = false;
  const processProof = {binary, sha256: expected, bytes: info.size, cwd, cwdRemoved: false, parentUID: typeof process.getuid === 'function' ? process.getuid() : null, nativeIdentity: null, artifactMode: info.mode & 0o777, artifactUID: info.uid, pid: null, closed: false, joined: false, forced: false, code: null, signal: null, metadataHTML: options.html !== false, fixtureMode: options.mode || '', termSent: false, shutdownRequested: false, unexpectedExit: false, environmentPolicy: 'remove all NEXTGO_DEMO_*, Lambda adapters and NEXTGO_ENV_DIR; explicit empty owned env directory'};
  const boundedLog = data => {logs.push(String(data)); while (logs.join('').length > 128 << 10) logs.shift();};
  const listen = (server, port) => {
    const controller = new AbortController();
    const state = {port, controller, started: false, bindJoined: false, listening: false, closed: false, failedBind: false, errors: [], abortReason: null};
    let resolveStart, rejectStart, resolveClosed, startSettled = false, abortTimer;
    const started = new Promise((resolve, reject) => {resolveStart = resolve; rejectStart = reject;});
    state.closedJob = new Promise(resolve => {resolveClosed = resolve;});
    listeners.set(server, state);
    const settleStart = (error) => {
      if (startSettled) return;
      startSettled = true; clearTimeout(abortTimer);
      startup.removeEventListener('abort', onStartupAbort);
      if (error) rejectStart(error); else resolveStart();
    };
    const forceClosedBind = () => {
      // Even a late listening event after an aborted startup stays owned.
      if (server.listening) server.close();
      server.closeAllConnections();
    };
    state.cancel = reason => {
      state.abortReason ||= reason;
      if (!controller.signal.aborted) controller.abort(reason);
      forceClosedBind();
      if (!startSettled && !abortTimer) abortTimer = setTimeout(() => {
        settleStart(new Error('Aborted metadata listen did not physically settle within 2.5s'));
        // Do not mark joined or remove the error/listening guards on timeout.
      }, 2500);
    };
    const onStartupAbort = () => state.cancel(startup.reason);
    server.on('error', error => {
      state.errors.push(String(error));
      if (!state.started) {
        state.failedBind = true; state.bindJoined = true;
        // A non-aborted failed bind owns no listener. An aborted bind still
        // waits for close, preserving cancellation's physical settlement proof.
        if (!state.abortReason) {state.closed = true; resolveClosed(); settleStart(error);}
        else forceClosedBind();
      }
    });
    server.on('listening', () => {
      state.started = true; state.listening = true; state.closed = false; state.bindJoined = true;
      if (state.abortReason || controller.signal.aborted || state.failedBind || closePromise) {forceClosedBind(); return;}
      // The per-listen controller remains owned, but the startup deadline can
      // never close a successfully started server 20s later.
      settleStart();
    });
    server.once('close', () => {
      state.closed = true; state.listening = false; state.bindJoined = true;
      resolveClosed(); settleStart(state.abortReason || new Error('Metadata listener closed before startup completed'));
    });
    if (startup.aborted) {
      state.abortReason = startup.reason; state.bindJoined = true; state.closed = true;
      resolveClosed(); settleStart(startup.reason); return started;
    }
    startup.addEventListener('abort', onStartupAbort, {once: true});
    try {server.listen({port, host: '127.0.0.1', signal: controller.signal});}
    catch (error) {state.failedBind = true; state.bindJoined = true; state.closed = true; resolveClosed(); settleStart(error);}
    return started;
  };
  function arm() {
    if (queue.length >= 16 || source.length >= 64) throw new Error('Metadata data-source gate capacity exceeded');
    const gate = {entry: null, released: false, release() {gate.released = true; gate.respond?.();}};
    queue.push(gate); return gate;
  }
  dataServer = createServer((req, res) => {
    if (req.headers.host !== '127.0.0.1:4162' || req.url !== '/value' || req.method !== 'GET') {res.writeHead(400).end(); return;}
    if (source.length >= 64) {res.writeHead(503).end(); return;}
    const gate = queue.shift(), entry = {index: source.length, unexpected: !gate, enteredAt: performance.now(), released: false, canceled: false, finished: false};
    source.push(entry);
    res.on('close', () => {if (!res.writableFinished) {entry.canceled = true; entry.canceledAt = performance.now();}});
    if (!gate) {res.writeHead(503, {'Content-Type': 'application/json'}).end('{"error":"unarmed fixture data request"}'); return;}
    gate.entry = entry;
    gate.respond = () => {
      if (entry.finished || entry.canceled || res.destroyed) return;
      entry.released = true;
      res.writeHead(200, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'});
      res.end(JSON.stringify(finalData), () => {entry.finished = true;});
    };
    if (gate.released) gate.respond();
  });
  let spawnError, nativeClosed = Promise.resolve();
  function spawnNative() {
    const childEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_ENV_DIR'));
    native = spawn(binary, ['-addr', '127.0.0.1:4161', '-env-dir', cwd], {cwd, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'], env: {...childEnv, GOMAXPROCS: '1', NEXTGO_DEMO_METADATA_SOURCE: 'http://127.0.0.1:4162/value', NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT: '30s', NEXTGO_DEMO_STREAM_TIMEOUT: '60s', NEXTGO_DEMO_METADATA_HTML: options.html === false ? 'false' : 'true', NEXTGO_DEMO_METADATA_FIXTURE: 'true', NEXTGO_DEMO_METADATA_TEST_MODE: options.mode || ''}});
    processProof.pid = native.pid ?? null;
    native.stdout.on('data', boundedLog); native.stderr.on('data', boundedLog);
    native.once('error', error => {spawnError = error;});
    nativeClosed = new Promise(resolve => native.once('close', (code, signal) => {Object.assign(processProof, {closed: true, code, signal, unexpectedExit: !processProof.shutdownRequested}); resolve();}));
  }
  gateway = createServer((req, res) => {
    if (req.headers.host !== '127.0.0.1:4160' || !req.url?.startsWith('/') || req.url.startsWith('//') || req.url.includes('\\')) {res.writeHead(400).end(); return;}
    if (wire.length >= 256) {res.writeHead(503).end(); return;}
    const record = {id: wire.length, method: req.method, path: req.url, metadataRequest: req.headers['x-nextgo-metadata'] ?? null, purpose: req.headers['x-nextgo-purpose'] ?? null, headersReceived: false, ended: false, closed: false, status: null, headers: {}, frames: [], bytes: 0, observerError: '', lateHeader: false, setCookie: false};
    wire.push(record);
    const upstream = httpRequest({hostname: '127.0.0.1', port: 4161, method: req.method, path: req.url, headers: {...req.headers, 'accept-encoding': 'identity'}}, actual => {
      record.headersReceived = true; record.status = actual.statusCode; record.lateHeader = actual.headers['x-metadata-late'] !== undefined; record.setCookie = actual.headers['set-cookie'] !== undefined;
      for (const name of ['content-type','cache-control','vary','x-nextgo-metadata','x-nextgo-protocol','x-nextgo-configuration']) if (actual.headers[name] !== undefined) record.headers[name] = actual.headers[name];
      res.writeHead(actual.statusCode, actual.headers);
      let pending = Buffer.alloc(0), frameCount = 0;
      const stream = String(actual.headers['content-type']).startsWith('application/x-nextgo-stream+json');
      actual.on('data', chunk => {
        record.bytes += chunk.length;
        if (!stream || record.observerError) return;
        if (record.bytes > 8 << 20 || pending.length + chunk.length > 4 << 20) {record.observerError = 'wire observer byte bound'; return;}
        // Assemble complete raw-byte lines before fatal UTF-8 decoding; never
        // decode individual TCP chunks (the public payload contains Unicode).
        pending = Buffer.concat([pending, chunk]);
        for (;;) {
          const end = pending.indexOf(10); if (end < 0) break;
          if (end > 2 << 20 || ++frameCount > 131) {record.observerError = 'wire observer frame bound'; return;}
          try {
            const frame = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(pending.subarray(0, end)));
            record.frames.push({keys: Object.keys(frame).sort(), metadataKeys: frame.metadata ? Object.keys(frame.metadata).sort() : undefined, type: frame.type, version: frame.version, failed: frame.failed === true, metadataPending: frame.metadataPending, metadataOwner: frame.metadataOwner, metadataIndices: frame.metadataIndices, owner: frame.metadata?.owner, title: frame.metadata?.title, metadataVersion: frame.metadata?.version, elements: frame.metadata?.elements?.length, pageTitle: frame.page?.title, pageBuild: frame.page?.build, pageConfiguration: frame.page?.configuration, pageURL: frame.page?.url});
          } catch {record.observerError = 'invalid genuine stream JSON'; return;}
          pending = pending.subarray(end + 1);
        }
        if (pending.length > 2 << 20) record.observerError = 'wire observer incomplete frame bound';
      });
      actual.on('end', () => {record.ended = true; if (stream && pending.length) record.observerError ||= 'truncated genuine stream';});
      actual.on('close', () => {record.closed = true;});
      actual.on('error', () => res.destroy()); actual.pipe(res);
    });
    upstreams.add(upstream); upstream.on('close', () => upstreams.delete(upstream));
    upstream.on('error', () => {record.closed = true; res.destroy();});
    let received = 0; req.on('data', chunk => {received += chunk.length; if (received > 64 << 10) {upstream.destroy(); req.destroy();}});
    req.on('aborted', () => upstream.destroy()); res.on('close', () => {if (!res.writableFinished) upstream.destroy();}); req.pipe(upstream);
  });
  gateway.on('connection', socket => {sockets.add(socket); socket.on('close', () => sockets.delete(socket));});
  const fixture = {
    origin, arm, source, wire,
    snapshot() {return {process: {...processProof}, source: source.map(entry => ({...entry})), wire: wire.map(record => ({...record})), gatewayListening: Boolean(gateway?.listening), sourceListening: Boolean(dataServer?.listening), httpJoined, listeners: [...listeners.values()].map(state => ({port: state.port, started: state.started, bindJoined: state.bindJoined, listening: state.listening, closed: state.closed, failedBind: state.failedBind, errors: [...state.errors]})), upstreams: upstreams.size, sockets: sockets.size, logs: logs.join('')};},
    close() {
      if (closePromise) return closePromise;
      closePromise = (async () => {
        processProof.shutdownRequested = true;
        for (const gate of queue.splice(0)) gate.release();
        for (const upstream of upstreams) upstream.destroy();
        const closeHTTP = server => {
          const state = listeners.get(server);
          if (!state) return Promise.resolve(); // No listen call ever started.
          if (!state.closed) state.cancel(new Error('Owned metadata fixture shutdown'));
          return state.closedJob;
        };
        const httpClosed = Promise.all([closeHTTP(gateway), closeHTTP(dataServer)]);
        for (const socket of sockets) socket.destroy();
        let timer;
        const terminate = signal => {if (!native?.pid || processProof.closed) return; if (process.platform === 'win32') native.kill(signal); else {try {process.kill(-native.pid, signal);} catch (error) {if (error.code !== 'ESRCH') throw error;}}};
        processProof.termSent = Boolean(native?.pid && !processProof.closed); terminate('SIGTERM');
        try {await Promise.race([nativeClosed, new Promise(resolve => {timer = setTimeout(resolve, 2500);})]);} finally {clearTimeout(timer);}
        if (native && !processProof.closed) {processProof.forced = true; terminate('SIGKILL'); await Promise.race([nativeClosed, new Promise((_, reject) => {timer = setTimeout(() => reject(new Error('Native process did not close after kill')), 1000);})]).finally(() => clearTimeout(timer));}
        await nativeClosed; processProof.joined = true;
        await Promise.race([httpClosed, new Promise((_, reject) => {timer = setTimeout(() => reject(new Error('Metadata fixture HTTP sockets did not close within bound')), 2500);})]).finally(() => clearTimeout(timer));
        httpJoined = true;
        const current = await readFile(binary); if (hash(current) !== expected) throw new Error('Artifact changed during browser case');
        await rm(cwd, {recursive: true, force: true});
        try {await lstat(cwd); throw new Error('Owned native cwd remains after removal');}
        catch (error) {if (error.code !== 'ENOENT') throw error; processProof.cwdRemoved = true;}
      })(); return closePromise;
    },
  };
  try {
    startup.throwIfAborted(); await listen(dataServer, 4162); startup.throwIfAborted(); spawnNative();
    for (;;) {
      startup.throwIfAborted();
      if (spawnError || processProof.closed) throw spawnError || new Error('Native server exited before readiness');
      let healthy = false;
      try {const response = await fetch('http://127.0.0.1:4161/api/health', {signal: AbortSignal.any([startup, AbortSignal.timeout(250)])}); healthy = response.ok; await response.body?.cancel();} catch (error) {if (startup.aborted) throw error;}
      if (healthy) {processProof.nativeIdentity = await observeNativeIdentity(native); await listen(gateway, 4160); return fixture;}
      await pause(50, startup);
    }
  } catch (error) {
    let failure = error;
    try {await fixture.close();} catch (cleanup) {failure = new AggregateError([error, cleanup], 'Metadata startup and owned cleanup failed');}
    failure.metadataFixtureProof = fixture.snapshot(); throw failure;
  }
}
