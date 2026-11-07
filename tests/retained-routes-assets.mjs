// Qualification helper only. The caller owns the already-running native server
// and its identity/closure proof. This module launches no process and fabricates
// no application response. Content-encoded gzip bytes require node:http: Fetch
// may transparently decompress them while leaving Content-Encoding intact.
import {createHash} from 'node:crypto';
import {request as httpRequest} from 'node:http';
import {gunzipSync} from 'node:zlib';

const MiB = 1 << 20;
const limits = Object.freeze({ssr: MiB, wasm: 64 * MiB, script: MiB, css: 256 << 10, headers: 16 << 10, headerCount: 64, headerValue: 8 << 10, milliseconds: 15_000});
const wasmMagic = Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]);
const digest = data => createHash('sha256').update(data).digest('hex');
const hex = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const check = (valid, message) => { if (!valid) throw Error('Asset proof: ' + message); };
function byteCap(maximum) { check(Number.isSafeInteger(maximum) && maximum > 0 && maximum <= limits.wasm, 'invalid byte cap'); }
function headersObject(value) {
  check(value && typeof value === 'object' && !Array.isArray(value) && [Object.prototype, null].includes(Object.getPrototypeOf(value)), 'invalid headers');
  const names = Object.keys(value); check(names.length <= limits.headerCount, 'header count bound');
  const result = Object.create(null); let bytes = 0;
  for (const name of names) {
    const text = value[name];
    check(/^[a-z0-9!#$%&'*+.^_`|~-]+$/.test(name) && typeof text === 'string' && Buffer.byteLength(text) <= limits.headerValue && !/[\x00-\x08\x0a-\x1f\x7f]/.test(text), 'invalid header field');
    bytes += Buffer.byteLength(name) + Buffer.byteLength(text) + 4; check(bytes <= limits.headers, 'header byte bound'); result[name] = text;
  }
  return result;
}
function wireHeaders(raw) {
  check(Array.isArray(raw) && raw.length % 2 === 0 && raw.length <= limits.headerCount * 2, 'raw header count bound');
  const result = Object.create(null), single = new Set(['content-type', 'content-encoding', 'content-length', 'transfer-encoding', 'connection']);
  for (let i = 0; i < raw.length; i += 2) {
    const name = raw[i].toLowerCase(), value = raw[i + 1];
    check(result[name] === undefined || !single.has(name), 'duplicate framing or representation header');
    result[name] = result[name] === undefined ? value : result[name] + ',' + value;
  }
  return headersObject(result);
}
function representation(headers, contentType, encoding, maximum) {
  byteCap(maximum);
  check(['text/html', 'application/wasm', 'text/javascript', 'text/css'].includes(contentType), 'unexpected expected MIME');
  check(headers['content-type']?.split(';', 1)[0].trim().toLowerCase() === contentType, 'unexpected MIME');
  const actual = headers['content-encoding']?.trim().toLowerCase();
  check(encoding === 'identity' ? !actual || actual === 'identity' : actual === 'gzip', 'unexpected content encoding');
}
function contentLength(headers, bytes, maximum) {
  if (headers['content-length'] === undefined) return;
  const raw = headers['content-length'];
  check(/^(0|[1-9][0-9]{0,9})$/.test(raw), 'noncanonical content length');
  const value = Number(raw); check(Number.isSafeInteger(value) && value <= maximum && value === bytes, 'content length mismatch or bound');
}
function varyEncoding(headers) {
  const values = headers.vary?.split(',').map(value => value.trim().toLowerCase());
  check(values && values.length <= 32 && values.every(value => /^[a-z0-9!#$%&'*+.^_`|~-]+$/.test(value)) && values.includes('accept-encoding'), 'Vary Accept-Encoding required');
}

// Pure audit seam. wire.data is the actual content-encoded entity read by
// node:http; closed may be true only after response/request/socket closure.
// The caller cannot substitute a previously decoded Fetch body.
export function validateGzipAsset(wire, identity, contentType, maximum) {
  byteCap(maximum);
  check(identity && hex(identity.sha256) && Number.isSafeInteger(identity.bytes) && identity.bytes > 0 && identity.bytes <= maximum, 'invalid identity proof');
  check(wire && wire.status === 200 && wire.complete === true && wire.closed === true && Buffer.isBuffer(wire.data), 'incomplete raw gzip exchange');
  const headers = headersObject(wire.headers), compressedMaximum = maximum + MiB;
  representation(headers, contentType, 'gzip', maximum); varyEncoding(headers);
  check(wire.data.length > 0 && wire.data.length <= compressedMaximum, 'compressed byte bound');
  contentLength(headers, wire.data.length, compressedMaximum);
  check(wire.data.length >= 3 && wire.data[0] === 0x1f && wire.data[1] === 0x8b && wire.data[2] === 8, 'gzip wire magic');
  const decoded = gunzipSync(wire.data, {maxOutputLength: maximum});
  const decodedSHA256 = digest(decoded);
  check(decoded.length === identity.bytes && decodedSHA256 === identity.sha256, 'gzip differs from identity representation');
  if (contentType === 'application/wasm') check(decoded.subarray(0, 8).equals(wasmMagic), 'decoded WASM magic');
  return {sha256: digest(wire.data), bytes: wire.data.length, decodedSHA256, decodedBytes: decoded.length, closed: true};
}

function ownedOrigin(origin) {
  check(typeof origin === 'string' && Buffer.byteLength(origin) <= 128, 'bounded origin required');
  let value; try { value = new URL(origin); } catch { throw Error('Asset proof: invalid origin'); }
  check(value.protocol === 'http:' && ['127.0.0.1', '[::1]'].includes(value.hostname) && /^(?:[1-9][0-9]{0,4})$/.test(value.port) && Number(value.port) <= 65535 &&
    !value.username && !value.password && !value.search && !value.hash && value.pathname === '/' && value.origin === origin && value.href === origin + '/', 'canonical explicit-port loopback HTTP origin required');
  return value.origin;
}
function ownedURL(origin, path) { const value = new URL(path, origin); check(value.origin === origin && value.protocol === 'http:' && value.pathname === path.split('?', 1)[0], 'asset URL escaped owned origin'); return value.href; }
async function identityHTTP(url, maximum, contentType) {
  byteCap(maximum);
  const response = await fetch(url, {redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(limits.milliseconds), headers: {'Accept-Encoding': 'identity'}});
  let reader;
  try {
    check(response.status === 200 && response.url === url && !response.redirected, 'actual same-origin HTTP 200 required');
    const headers = headersObject(Object.fromEntries(response.headers)); representation(headers, contentType, 'identity', maximum);
    if (contentType !== 'text/html') varyEncoding(headers);
    reader = response.body?.getReader(); check(reader, 'response body required');
    const parts = []; let bytes = 0;
    for (;;) {
      const part = await reader.read(); if (part.done) break;
      bytes += part.value.byteLength; check(bytes <= maximum, 'identity body byte bound'); parts.push(Buffer.from(part.value));
    }
    check(bytes > 0, 'empty identity body'); contentLength(headers, bytes, maximum);
    const data = Buffer.concat(parts, bytes); return {data, sha256: digest(data), bytes, headers};
  } finally {
    if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    else await response.body?.cancel().catch(() => {});
  }
}

// One absolute timer spans connect, headers, body and physical closure. We do
// not renew a socket inactivity timeout on data. Failure destroys only this
// request's response/socket and waits for their observed close before rejecting.
function gzipHTTP(url, maximum, contentType, identity) {
  return new Promise((resolve, reject) => {
    let request, response, socket, timer, failure, settled = false;
    let requestClosed = false, responseClosed = false, socketClosed = false, ended = false, headers, bytes = 0;
    const parts = [], compressedMaximum = maximum + MiB;
    const finish = () => {
      if (settled || !requestClosed || response && !responseClosed || socket && !socketClosed) return;
      if (!failure && (!response || !ended || !response.complete || !socket)) failure = Error('Asset proof: incomplete gzip close proof');
      settled = true; clearTimeout(timer);
      if (failure) { reject(failure); return; }
      try { resolve(validateGzipAsset({data: Buffer.concat(parts, bytes), status: response.statusCode, headers, complete: response.complete, closed: true}, identity, contentType, maximum)); }
      catch (error) { reject(error); }
    };
    const fail = error => {
      failure ||= error instanceof Error ? error : Error('Asset proof: raw HTTP failure');
      response?.destroy(failure); request?.destroy(failure); socket?.destroy(failure); finish();
    };
    timer = setTimeout(() => fail(Error('Asset proof: absolute gzip request deadline exceeded')), limits.milliseconds);
    try {
      request = httpRequest(url, {method: 'GET', agent: false, maxHeaderSize: limits.headers, headers: {'Accept-Encoding': 'gzip', Connection: 'close'}}, value => {
        response = value;
        response.once('error', fail);
        response.once('aborted', () => fail(Error('Asset proof: gzip response aborted')));
        response.once('close', () => { responseClosed = true; if (!ended && !failure) fail(Error('Asset proof: gzip response closed before end')); finish(); });
        response.once('end', () => { ended = true; if (!response.complete) fail(Error('Asset proof: incomplete gzip message')); finish(); });
        response.on('data', part => {
          if (failure) return;
          bytes += part.length;
          if (bytes > compressedMaximum) { fail(Error('Asset proof: compressed byte bound')); return; }
          parts.push(part);
        });
        try { headers = wireHeaders(response.rawHeaders); check(response.statusCode === 200, 'gzip HTTP 200 required'); representation(headers, contentType, 'gzip', maximum); varyEncoding(headers); }
        catch (error) { fail(error); }
      });
      request.once('error', fail);
      request.once('close', () => { requestClosed = true; if (!response && !failure) fail(Error('Asset proof: request closed without response')); finish(); });
      request.once('socket', value => {
        socket = value; socket.once('error', fail); socket.once('close', () => { socketClosed = true; finish(); });
        if (failure) socket.destroy(failure);
      });
      request.end();
    } catch (error) {
      if (!request) { settled = true; clearTimeout(timer); reject(error); }
      else fail(error);
    }
  });
}

export async function probeRuntimeAssets(origin, app, expectedRuntimeSHA256) {
  origin = ownedOrigin(origin);
  check(app === 'retained' || app === 'merge', 'app must be retained or merge'); check(hex(expectedRuntimeSHA256), 'selected SDK runtime SHA256 required');
  const html = await identityHTTP(ownedURL(origin, app === 'retained' ? '/a' : '/?q=a'), limits.ssr, 'text/html');
  const text = new TextDecoder('utf-8', {fatal: true}).decode(html.data);
  const prefixes = [...text.matchAll(/\bsrc="(\/_nextgo\/[a-f0-9]{20}\/)wasm_exec\.js"/g)].map(match => match[1]);
  check(prefixes.length === 1, 'one canonical real SSR runtime prefix required');
  const prefix = prefixes[0], boot = [...text.matchAll(/\bsrc="(\/_nextgo\/[a-f0-9]{20}\/)boot\.js"/g)].map(match => match[1]);
  check(boot.length === 1 && boot[0] === prefix, 'bootstrap must use the same canonical runtime prefix');
  const result = {ssr: {sha256: html.sha256, bytes: html.bytes, prefix}, assets: {}};
  // Sequential bounded reads keep ownership simple and do not add processes.
  for (const [name, path, contentType, maximum] of [
    ['app.wasm', prefix + 'app.wasm', 'application/wasm', limits.wasm],
    ['wasm_exec.js', prefix + 'wasm_exec.js', 'text/javascript', limits.script],
    ['boot.js', prefix + 'boot.js', 'text/javascript', limits.script],
    ['css', '/assets/' + app + '.css', 'text/css', limits.css],
  ]) {
    let value = await identityHTTP(ownedURL(origin, path), maximum, contentType);
    if (name === 'app.wasm') check(value.data.subarray(0, 8).equals(wasmMagic), 'identity WASM magic');
    if (name === 'wasm_exec.js') check(value.sha256 === expectedRuntimeSHA256, 'served runtime differs from selected SDK');
    const asset = {sha256: value.sha256, bytes: value.bytes, headers: value.headers}; value = null;
    asset.gzip = await gzipHTTP(ownedURL(origin, path), maximum, contentType, asset); result.assets[name] = asset;
  }
  return result;
}
