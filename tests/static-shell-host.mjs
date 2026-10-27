// Development/test adapter for reviewed, prebuilt exports. No application
// server, subprocess, compiler, download, or artifact mutation is supported.
import {createServer} from 'node:http';
import {createConnection} from 'node:net';
import {constants} from 'node:fs';
import {lstat, readdir, realpath, open} from 'node:fs/promises';
import {isAbsolute, join, relative, sep, posix} from 'node:path';
import {createHash} from 'node:crypto';

const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const hex64 = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const check = (condition, message) => {if (!condition) throw new Error(message);};
const maximumFile = 128 << 20, maximumTotal = 512 << 20;
const types = {'.html':'text/html; charset=utf-8', '.json':'application/json', '.wasm':'application/wasm', '.js':'application/javascript', '.mjs':'application/javascript', '.cjs':'application/javascript', '.css':'text/css', '.map':'application/json', '.svg':'image/svg+xml', '.png':'image/png', '.jpg':'image/jpeg', '.jpeg':'image/jpeg', '.gif':'image/gif', '.ico':'image/x-icon', '.webp':'image/webp', '.woff':'font/woff', '.woff2':'font/woff2', '.txt':'text/plain; charset=utf-8', '.xml':'application/xml', '.webmanifest':'application/manifest+json'};
// Root-approved finite V1 virtual document policy, after one decoding pass.
// Other dots are permitted: notes.v2 and notes.json/details remain documents.
const resourceSuffix = /\.(?:json|wasm|css|js|mjs|cjs|map)$/i;
const descriptorKeys = ['version','build','configuration','pattern','component','seedURL','envelopeURL','sha256','bytes'];

function pathParts(raw) {
  check(typeof raw === 'string' && raw.length <= 8192 && raw.startsWith('/') && !raw.startsWith('//') && !/[\\\x00-\x20\x7f#?]/.test(raw), 'invalid raw path');
  const logical = raw.length > 1 && raw.endsWith('/') ? raw.slice(0, -1) : raw;
  const parts = logical === '/' ? [] : logical.slice(1).split('/').map(part => {
    const decoded = decodeURIComponent(part);
    check(decoded !== '' && decoded !== '.' && decoded !== '..' && !/[\/\\\x00-\x1f\x7f]/.test(decoded), 'invalid decoded segment');
    return decoded;
  });
  return {raw, logical, parts, decoded: '/' + parts.join('/')};
}
function literalBase(base) {
  check(typeof base === 'string' && base.length <= 8192 && (base === '' || /^\/(?:[A-Za-z0-9._~-]+\/)*[A-Za-z0-9._~-]+$/.test(base)), 'invalid literal base');
  if (base) pathParts(base);
  return base;
}
function strip(base, raw) {
  if (!base) return raw;
  if (raw === base) return '/';
  return raw.startsWith(base + '/') ? raw.slice(base.length) : null;
}
function documentURL(base, local, trailing) {
  if (local === '/') return base ? base + (trailing ? '/' : '') : '/';
  return base + local + (trailing && !posix.extname(local) ? '/' : '');
}
function attr(html, name) {
  const bodies = [...html.matchAll(/<body\s[^>]*>/g)];
  check(bodies.length === 1, 'expected one native body');
  const values = [...bodies[0][0].matchAll(new RegExp('(?:^|\\s)' + name + '="([^"<>]*)"', 'g'))];
  check(values.length === 1, 'missing or duplicate native body attribute: ' + name);
  return values[0][1].replace(/&(quot|#34|#39|apos|lt|gt|amp);/g, (_, key) => ({quot:'"','#34':'"','#39':"'",apos:"'",lt:'<',gt:'>',amp:'&'}[key]));
}
async function readGuarded(root, name, expected, operations) {
  const work = {file:name, phase:'path-guards'};
  operations?.active.add(work);
  let file;
  try {
    const target = join(root, ...name.split('/'));
    let current = root;
    for (const part of name.split('/')) {
      current = join(current, part);
      const info = await lstat(current);
      check(!info.isSymbolicLink(), 'artifact symlink rejected');
    }
    check(await realpath(target) === target, 'artifact path changed');
    work.phase = 'open';
    file = await open(target, constants.O_RDONLY | constants.O_NOFOLLOW);
    work.phase = 'read-and-stat';
    const info = await file.stat();
    check(info.isFile() && info.nlink === 1 && info.size <= maximumFile, 'artifact is not a bounded independent regular file');
    const buffer = Buffer.allocUnsafe(info.size + 1);
    let used = 0;
    while (used < buffer.length) {
      const {bytesRead} = await file.read(buffer, used, buffer.length - used, used);
      if (!bytesRead) break;
      used += bytesRead;
    }
    const after = await file.stat();
    check(used === info.size && after.size === info.size && after.mtimeMs === info.mtimeMs, 'artifact changed while reading');
    const bytes = buffer.subarray(0, used);
    const entry = {bytes:bytes.length, sha256:sha(bytes), type:types[posix.extname(name).toLowerCase()] || 'application/octet-stream'};
    if (expected) check(entry.bytes === expected.bytes && entry.sha256 === expected.sha256, 'approved artifact changed');
    return {entry, bytes};
  } finally {
    try {
      if (file) {work.phase = 'file-close'; await file.close();}
    } catch (error) {
      operations?.closeErrors.push({file:name, error:String(error.message).slice(0,160)});
      throw error;
    } finally {operations?.active.delete(work);}
  }
}

export function artifactFromEnvironment(variant) {
  check(['EAGER_ROOT','EAGER_MOUNTED','LAZY_ROOT','LAZY_MOUNTED'].includes(variant), 'unknown artifact variant');
  const prefix = 'NEXTGO_STATIC_SHELL_' + variant;
  return {variant, root:process.env[prefix + '_EXPORT'], manifestSHA256:process.env[prefix + '_MANIFEST_SHA256']};
}

export async function validateStaticArtifact(input) {
  check(input && isAbsolute(input.root || '') && hex64(input.manifestSHA256), 'explicit absolute export root and 64-lowerhex manifest SHA-256 required');
  const root = input.root;
  check(await realpath(root) === root && (await lstat(root)).isDirectory(), 'export root must be a real directory without symlink aliases');
  const inventory = new Map();
  let total = 0;
  async function walk(directory, depth) {
    check(depth <= 16, 'artifact directory depth exceeded');
    for (const entry of await readdir(directory, {withFileTypes:true})) {
      check(entry.name !== '.' && entry.name !== '..' && !/[\\\x00-\x1f\x7f]/.test(entry.name), 'invalid artifact filename');
      const path = join(directory, entry.name), info = await lstat(path);
      check(!info.isSymbolicLink(), 'artifact tree symlink rejected');
      if (info.isDirectory()) {await walk(path, depth + 1); continue;}
      check(info.isFile() && inventory.size < 4096 && info.size <= maximumFile, 'artifact file bound exceeded');
      const name = relative(root, path).split(sep).join('/');
      const actual = await readGuarded(root, name);
      total += actual.entry.bytes;
      check(total <= maximumTotal, 'artifact total size exceeded');
      inventory.set(name, actual.entry);
    }
  }
  await walk(root, 0);
  const pinned = inventory.get('.nextgo-export.json');
  check(pinned && pinned.bytes <= 32 << 20 && pinned.sha256 === input.manifestSHA256, 'actual publication manifest pin mismatch');
  const manifest = JSON.parse((await readGuarded(root, '.nextgo-export.json', pinned)).bytes.toString('utf8'));
  check(manifest.format === 'nextgo-static-v3' && manifest.protocol === 3 && /^[a-f0-9]{20}$/.test(manifest.build) && hex64(manifest.configuration), 'unsupported publication identity');
  check(!('scopes' in manifest) && !('domain' in manifest), 'fixture requires one unlocalized ordinary scope');
  const base = literalBase(manifest.basePath || '');
  check((manifest.assetPrefix || '') === '' || manifest.assetPrefix === base, 'fixture requires same-origin colocated runtime assets');
  check(manifest.trailingSlash === undefined || typeof manifest.trailingSlash === 'boolean', 'invalid trailing-slash policy');
  check(Array.isArray(manifest.paths) && manifest.paths.length === 2 && [...manifest.paths].sort().join('|') === '/|/workspace', 'sample must export exactly home and zero-tail seed');
  const mode = input.variant?.startsWith('LAZY_') ? 'workspacelazy' : 'workspace';
  if (input.variant) check(input.variant.endsWith('_MOUNTED') ? base !== '' : base === '', 'artifact variant/base mismatch');
  const descriptor = manifest.staticShell;
  check(descriptor && Object.keys(descriptor).sort().join('|') === [...descriptorKeys].sort().join('|'), 'descriptor fields mismatch');
  check(descriptor.version === 1 && descriptor.build === manifest.build && descriptor.configuration === manifest.configuration && descriptor.pattern === '/workspace/*rest?' && descriptor.component === mode && hex64(descriptor.sha256) && Number.isInteger(descriptor.bytes) && descriptor.bytes > 0 && descriptor.bytes <= 8 << 20, 'descriptor identity or sample component mismatch');
  check(Buffer.byteLength(JSON.stringify(descriptor)) <= 16 << 10, 'descriptor bound exceeded');
  check(descriptor.seedURL === documentURL(base, '/workspace', manifest.trailingSlash) && descriptor.envelopeURL === base + '/_nextgo/' + manifest.build + '/static-shell/' + descriptor.sha256 + '.json', 'descriptor derived URLs mismatch');
  const envelopeName = pathParts(descriptor.envelopeURL).decoded.slice(1);
  const envelope = inventory.get(envelopeName);
  check(envelope && envelope.sha256 === descriptor.sha256 && envelope.bytes === descriptor.bytes, 'actual immutable envelope digest/size mismatch');
  const actualEnvelope = JSON.parse((await readGuarded(root, envelopeName, envelope)).bytes.toString('utf8'));
  check(actualEnvelope.version === 3 && actualEnvelope.status === 200 && actualEnvelope.url === descriptor.seedURL && actualEnvelope.build === manifest.build && actualEnvelope.configuration === manifest.configuration, 'actual seed envelope identity mismatch');
  check(typeof actualEnvelope.html === 'string' && actualEnvelope.html.includes('data-shell-fallback') && (actualEnvelope.html.match(/data-nextgo-island=/g) || []).length === 1 && actualEnvelope.html.includes('data-nextgo-island="' + mode + '"'), 'seed does not contain one matching actual native island');
  const documents = new Map();
  for (const local of manifest.paths) {
    const url = documentURL(base, local, manifest.trailingSlash);
    const name = [base.slice(1), local.slice(1), 'index.html'].filter(Boolean).join('/');
    check(inventory.has(name), 'declared native HTML missing');
    documents.set(pathParts(url).logical, name);
  }
  let htmlFiles = 0;
  for (const [name, entry] of inventory) {
    if (!name.endsWith('.html')) continue;
    htmlFiles++;
    const html = (await readGuarded(root, name, entry)).bytes.toString('utf8');
    check(attr(html, 'data-nextgo-export') === 'true' && attr(html, 'data-nextgo-build') === manifest.build && attr(html, 'data-nextgo-configuration') === manifest.configuration, 'retained HTML generation mismatch');
    const embedded = JSON.parse(attr(html, 'data-nextgo-static-shell'));
    check(descriptorKeys.every(key => embedded[key] === descriptor[key]) && Object.keys(embedded).length === descriptorKeys.length, 'retained HTML descriptor mismatch');
  }
  check(htmlFiles === 2, 'unexpected sample HTML inventory');
  check([...inventory.keys()].some(name => name.endsWith('/app.wasm')), 'actual core WASM missing');
  check([...inventory.keys()].some(name => /\/module-[a-f0-9]{64}\.wasm$/.test(name)), 'actual lazy WASM missing from discovery');
  return {root, manifest, descriptor, inventory, documents, base, mode, envelopeName, seedName:documents.get(pathParts(descriptor.seedURL).logical), manifestSHA256:input.manifestSHA256, inventorySHA256:sha(Buffer.from(JSON.stringify([...inventory]))), totalBytes:total};
}

function documentRequest(request) {
  const destination = request.headers['sec-fetch-dest'];
  if (destination && destination !== 'document') return false;
  return String(request.headers.accept || '').split(',').some(value => {
    const [media, ...parameters] = value.trim().toLowerCase().split(';').map(part => part.trim());
    if (media !== 'text/html') return false;
    const quality = parameters.find(part => part.startsWith('q='));
    return quality === undefined || /^q=(?:0(?:\.\d{0,3})?|1(?:\.0{0,3})?)$/.test(quality) && Number(quality.slice(2)) > 0;
  });
}
async function refused(port, state) {
  return await new Promise(resolve => {
    const probe = createConnection({host:'127.0.0.1', port});
    state.socket = probe; state.started = true;
    probe.once('connect', () => {state.result = 'CONNECTED'; probe.destroy();});
    probe.once('error', error => {state.result = error.code;});
    probe.once('close', () => {state.closeJoined = true; resolve(state.result);});
  });
}

export async function startStaticShellHost(input, {leaseMS} = {}) {
  check(Number.isInteger(leaseMS) && leaseMS >= 1000 && leaseMS <= 120000, 'explicit finite static-host lease 1000..120000ms required');
  const artifact = await validateStaticArtifact(input); // All validation precedes listen.
  const sockets = new Set(), responses = new Set(), handlers = new Set(), records = [];
  const operations = {active:new Set(), closeErrors:[]};
  const listenerErrors = [];
  let overflow = 0, fault = null, closing, port, expired = false, closedProof = null;
  function record(value) {if (records.length < 512) records.push(value); else overflow++;}
  const server = createServer((request, response) => {
    const job = serve(request, response).catch(error => listenerErrors.push(String(error.message).slice(0,160))).finally(() => handlers.delete(job));
    handlers.add(job);
  });
  async function serve(request, response) {
    responses.add(response);
    response.once('close', () => responses.delete(response));
    request.resume();
    const entry = {method:request.method, target:String(request.url || '').slice(0, 1024), status:null, file:null, fault:null, mime:null};
    record(entry);
    const send = (status, body, type = 'text/plain; charset=utf-8') => {
      entry.status = status; entry.mime = type;
      if (response.destroyed || response.writableEnded) return;
      response.writeHead(status, {'Content-Type':type, 'Content-Length':body.length, 'Cache-Control':'no-store', 'X-Content-Type-Options':'nosniff'});
      response.end(request.method === 'HEAD' ? undefined : body);
    };
    try {
      if (overflow || closing) {send(503, Buffer.from('fixture unavailable')); return;}
      if (request.method !== 'GET' && request.method !== 'HEAD') {send(405, Buffer.from('method denied')); return;}
      check(typeof request.url === 'string' && request.url.length <= 16384 && !request.url.includes('#'), 'invalid request target');
      const path = pathParts(request.url.split('?')[0]);
      const local = strip(artifact.base, path.raw);
      if (local === null) {send(404, Buffer.from('literal mount miss')); return;}
      // Exact retained files do not create aliases for ownership/framework
      // prefixes. Encoded basenames after a literal prefix remain supported.
      const localParts = pathParts(local);
      const first = localParts.parts[0];
      if (['workspace','_nextgo','assets','api','.well-known'].includes(first) && !(local === '/' + first || local.startsWith('/' + first + '/'))) {send(404, Buffer.from('literal namespace miss')); return;}
      let name = artifact.inventory.has(path.decoded.slice(1)) ? path.decoded.slice(1) : artifact.documents.get(path.logical);
      // Exact files/documents win before virtual resource admission.
      if (!name) {
        if (/^\/(?:api|_nextgo|assets|\.well-known)(?:\/|$)/.test(local) || local === '/.nextgo-export.json' || !documentRequest(request)) {send(404, Buffer.from('static miss')); return;}
        const selected = pathParts(local);
        const shellOwned = selected.logical === '/workspace' || selected.logical.startsWith('/workspace/');
        if (!shellOwned || resourceSuffix.test(selected.parts.at(-1) || '')) {send(404, Buffer.from('static miss')); return;}
        name = artifact.seedName;
      }
      // The inventory is fixed. Re-read and rehash without following links so
      // externally changed bytes cannot silently become fixture responses.
      const actual = await readGuarded(artifact.root, name, artifact.inventory.get(name), operations);
      entry.file = name;
      let body = actual.bytes, type = actual.entry.type, status = 200;
      if (fault && name === artifact.envelopeName) {
        entry.fault = fault;
        if (fault === 'missing') {status = 404; body = Buffer.from('deliberate immutable miss'); type = 'text/plain; charset=utf-8';}
        if (fault === 'mime') type = 'text/plain; charset=utf-8';
        if (fault === 'corrupt') {body = Buffer.from(body); body[0] ^= 1;}
      }
      send(status, body, type);
    } catch (error) {
      entry.error = String(error.message).slice(0, 160);
      if (!response.headersSent) send(error instanceof URIError || /path|segment|target/.test(error.message) ? 400 : 500, Buffer.from('static fixture rejection'));
      else response.destroy();
    }
  }
  server.headersTimeout = 5000; server.requestTimeout = 10000; server.keepAliveTimeout = 1000;
  server.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
  server.on('error', error => listenerErrors.push(String(error.message).slice(0,160)));
  let timer;
  const fixture = {
    artifact, records,
    origin:null,
    setEnvelopeFault(value) {check(value === null || ['missing','mime','corrupt'].includes(value), 'unknown deliberate response fault'); fault = value;},
    snapshot() {return {topology:'in-process prebuilt static files only', ownerPID:process.pid, root:artifact.root, manifestSHA256:artifact.manifestSHA256, inventorySHA256:artifact.inventorySHA256, files:artifact.inventory.size, bytes:artifact.totalBytes, component:artifact.mode, base:artifact.base, listening:server.listening, sockets:sockets.size, responseStreams:responses.size, handlers:handlers.size, fileOperations:[...operations.active].map(work => ({...work})), fileCloseErrors:[...operations.closeErrors], records:records.map(entry => ({...entry})), overflow, listenerErrors:[...listenerErrors], expired, closure:closedProof};},
    async close({join = null} = {}) {
      closing ||= (async () => {
        clearTimeout(timer);
        // One immutable absolute deadline covers every cleanup phase. Expiry
        // reports actual pending work; the qualification controller must then
        // terminate this Node worker. A Promise race never proves a join.
        const startedAt = Date.now(), deadline = startedAt + 5000;
        const state = {serverCloseRequested:false, serverCallbackJoined:false, socketJoinsComplete:false, handlerJoinsComplete:false, additionalJoinRequested:join !== null, additionalJoinComplete:join === null, timedOut:false, failed:false};
        let joinError;
        const suppliedJoin = join === null ? Promise.resolve() : Promise.resolve(join).then(() => {state.additionalJoinComplete = true;}, error => {state.additionalJoinComplete = true; joinError = error;});
        const probe = {started:false, closeJoined:false, result:null, socket:null};
        const proof = () => ({startedAt, deadline, budgetMS:5000, ...state, listening:server.listening, sockets:sockets.size, responseStreams:responses.size, handlers:handlers.size, fileOperations:[...operations.active].map(work => ({...work})), fileCloseErrors:[...operations.closeErrors], port, probe:{started:probe.started, closeJoined:probe.closeJoined, result:probe.result}, outerControllerTerminationRequired:state.timedOut || state.failed});
        let deadlineTimer;
        const expiry = new Promise(resolve => {deadlineTimer = setTimeout(() => {state.timedOut = true; probe.socket?.destroy(); resolve('deadline');}, Math.max(0, deadline - Date.now()));});
        const actual = (async () => {
          if (server.listening) {
            state.serverCloseRequested = true;
            const joinedServer = new Promise((resolve, reject) => server.close(error => {state.serverCallbackJoined = true; if (error) reject(error); else resolve();}));
            const joinedSockets = [...sockets].map(socket => new Promise(resolve => {if (socket.closed) {resolve(); return;} socket.once('close', resolve); socket.destroy();}));
            server.closeAllConnections();
            await Promise.all([joinedServer, ...joinedSockets]);
          }
          state.socketJoinsComplete = sockets.size === 0;
          await Promise.allSettled([...handlers]);
          state.handlerJoinsComplete = handlers.size === 0 && operations.active.size === 0;
          await suppliedJoin;
          if (joinError) throw joinError;
          if (state.timedOut || Date.now() >= deadline) return 'deadline';
          if (port) await refused(port, probe);
          return 'actual';
        })();
        try {
          const outcome = await Promise.race([actual, expiry]);
          if (outcome !== 'actual') state.timedOut = true;
          closedProof = proof();
          check(!state.timedOut && (!port || state.serverCallbackJoined && probe.closeJoined && probe.result === 'ECONNREFUSED') && state.socketJoinsComplete && state.handlerJoinsComplete && state.additionalJoinComplete && !server.listening && sockets.size === 0 && responses.size === 0 && handlers.size === 0 && operations.active.size === 0 && operations.closeErrors.length === 0, 'owned static cleanup deadline/join failed; inspect pending physical proof and terminate worker externally');
        } catch (error) {state.failed = true; closedProof = proof(); throw error;}
        finally {clearTimeout(deadlineTimer);}
      })();
      return closing;
    },
  };
  try {
    await new Promise((resolve, reject) => {server.once('error', reject); server.listen(0, '127.0.0.1', resolve);});
    port = server.address().port;
    fixture.origin = 'http://127.0.0.1:' + port;
    timer = setTimeout(() => {expired = true; void fixture.close().catch(error => listenerErrors.push(String(error.message)));}, leaseMS);
    timer.unref();
    return fixture;
  } catch (error) {
    try {await fixture.close();} catch (cleanup) {error.cleanupError = String(cleanup.message);}
    error.staticShellProof = fixture.snapshot();
    throw error;
  }
}
