// Real application admission fixture. The holder consumes an actual App stream
// slot until its cooperative deferred loader observes request cancellation.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'stream-admission');
await mkdir(output, { recursive: true });
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env: base });
  if (await new Promise(resolve => build.once('exit', resolve)) !== 0) process.exit(1);
}
const child = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4161'], { cwd: output, stdio: 'inherit', env: { ...base, NEXTGO_DEMO_STREAM_MAX_STREAMS: '1', NEXTGO_DEMO_STREAM_MAX_LOADERS: '1', NEXTGO_DEMO_STREAM_TIMEOUT: '30s', NEXTGO_DEMO_STREAM_HOLD: 'true' } });
let held, busy = false, stopping = false;
function json(response, status, value) { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(value)); }
async function release() {
  const previous = held; held = undefined;
  if (!previous) return;
  previous.expected = true; previous.request.destroy(); previous.response?.destroy();
  await previous.done;
}
async function hold(negotiated) {
  if (held) throw new Error('stream holder already active');
  const state = { expected: false, request: undefined, response: undefined };
  state.done = new Promise(resolve => { state.resolve = resolve; });
  held = state;
  await new Promise((resolve, reject) => {
    let shell = '', settled = false;
    const timer = setTimeout(() => { reject(new Error('stream holder did not receive a shell')); state.request.destroy(); }, 5000);
    state.request = requestHTTP({ hostname: '127.0.0.1', port: 4161, path: '/stream?hold=1', headers: negotiated ? { 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1' } : {} }, incoming => {
      state.response = incoming;
      incoming.on('error', error => { if (!state.expected && !settled) reject(error); });
      incoming.on('close', () => state.resolve());
      incoming.on('data', chunk => {
        if (settled) return;
        shell += chunk.toString('utf8');
        if (incoming.statusCode !== 200 || shell.length > 128 * 1024) { clearTimeout(timer); reject(new Error('invalid holder response')); state.request.destroy(); return; }
        if (shell.includes('data-stream-loading')) { settled = true; clearTimeout(timer); resolve(); }
      });
    });
    state.request.on('error', error => { clearTimeout(timer); state.resolve(); if (!state.expected && !settled) reject(error); });
    state.request.end();
  });
}
const control = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4162');
  if (url.pathname === '/_fixture/ready') { json(response, 200, { ready: true }); return; }
  if (request.method !== 'POST' || busy) { json(response, 409, { error: 'fixture operation unavailable' }); return; }
  busy = true;
  try {
    if (url.pathname === '/_fixture/hold') await hold(url.searchParams.get('negotiated') === 'true');
    else if (url.pathname === '/_fixture/release') await release();
    else { json(response, 404, { error: 'unknown fixture operation' }); return; }
    json(response, 200, { held: !!held });
  } catch (error) { await release(); console.error(error.message); json(response, 500, { error: 'fixture operation failed' }); }
  finally { busy = false; }
});
control.listen(4162, '127.0.0.1');
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => { stopping = true; void release(); control.closeAllConnections(); control.close(); child.kill('SIGTERM'); });
child.once('exit', code => { control.close(() => process.exit(stopping ? 0 : code || 1)); });
