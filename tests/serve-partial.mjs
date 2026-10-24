// Build-time providers are healthy; request-region providers are deliberately
// unavailable during build. Runtime reverses that condition for every replica.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'partial');
await mkdir(output, { recursive: true });
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env: { ...base, NEXTGO_DEMO_PARTIAL_REGION_FAIL: 'true' } });
  if (await new Promise(resolve => build.once('exit', resolve)) !== 0) process.exit(1);
}
const addresses = { first: 4164, broken: 4165, second: 4166 };
const children = [];
const gates = new Map();
let stopping = false;
const gateway = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4163');
  if (url.pathname === '/_fixture/release-region' && request.method === 'POST') {
    const gate = gates.get(url.searchParams.get('key'));
    if (!gate) { response.writeHead(404); response.end('region gate not found'); return; }
    gate.released = true; gate.response.write(gate.pending); gate.pending = '';
    if (gate.ended) gate.response.end();
    gates.delete(url.searchParams.get('key')); response.end('released'); return;
  }
  const replica = request.headers['x-fixture-partial'] || 'first';
  if (!Object.hasOwn(addresses, replica)) { response.writeHead(400); response.end('unknown fixture replica'); return; }
  const headers = { ...request.headers, host: '127.0.0.1:4163', 'accept-encoding': 'identity' }; delete headers['x-fixture-partial']; delete headers['x-fixture-partial-gate'];
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: addresses[replica], method: request.method, path: request.url, headers }, incoming => {
    response.writeHead(incoming.statusCode, incoming.headers); incoming.on('error', () => response.destroy());
    const key = request.headers['x-fixture-partial-gate'];
    if (!key || !String(incoming.headers['content-type']).includes('text/html') || !url.pathname.startsWith('/partial')) { incoming.pipe(response); return; }
    const gate = { response, pending: '', started: false, released: false, ended: false }; gates.set(key, gate);
    incoming.setEncoding('utf8');
    incoming.on('data', chunk => {
      if (gate.released) { response.write(chunk); return; }
      gate.pending += chunk;
      if (!gate.started) {
        // Preserve the real server's complete initial Flush, including its
        // bootstrap-ready marker. Splitting that marker into a tiny later write
        // can leave WebKit's incremental parser waiting behind this test gate.
        const marker = gate.pending.indexOf('<span id="nextgo-initial-ready"');
        const end = marker < 0 ? -1 : gate.pending.indexOf('</span>', marker);
        if (end >= 0) {
          const complete = end + '</span>'.length;
          response.write(gate.pending.slice(0, complete)); gate.pending = gate.pending.slice(complete); gate.started = true;
        }
      }
      if (gate.pending.length > (2 << 20)) response.destroy(new Error('fixture region gate overflow'));
    });
    incoming.on('end', () => { gate.ended = true; if (gate.released) response.end(); });
    response.on('close', () => gates.delete(key));
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('partial fixture upstream unavailable'); });
  request.on('aborted', () => upstream.destroy()); response.on('close', () => { if (!response.writableEnded) upstream.destroy(); }); request.pipe(upstream);
});
for (const [name, port] of Object.entries(addresses)) {
  const child = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', `127.0.0.1:${port}`], { cwd: output, stdio: 'inherit', env: { ...base, NEXTGO_DEMO_PARTIAL_SHELL_FAIL: 'true', NEXTGO_DEMO_PARTIAL_REGION_FAIL: name === 'broken' ? 'true' : 'false' } });
  children.push(child);
  child.once('exit', code => { if (!stopping) { console.error(`partial ${name} replica exited ${code}`); shutdown(code || 1); } });
}
function shutdown(code = 0) { if (stopping) return; stopping = true; process.exitCode = code; for (const gate of gates.values()) gate.response.destroy(); gateway.closeAllConnections(); gateway.close(); for (const child of children) child.kill('SIGTERM'); }
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => shutdown());
gateway.listen(4163, '127.0.0.1');
