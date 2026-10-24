// Test-only rolling proxy: real instances of one binary with different runtime
// configurations. The control endpoint and proxy are never part of the framework.
import { spawn } from 'node:child_process';
import { createServer, request } from 'node:http';
import { join } from 'node:path';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const variants = {
  old: { port: 4108, base: '', assets: '', slash: false },
  updated: { port: 4109, base: '', assets: '/v2-cdn', slash: true },
  moved: { port: 4110, base: '/new', assets: '/new-cdn', slash: true },
  'old-mounted': { port: 4111, base: '/old', assets: '', slash: false },
};
let active = 'old';
let closing = false;
const children = Object.values(variants).map(variant => spawn(binary, ['-addr', `127.0.0.1:${variant.port}`], {
  cwd: '/', stdio: 'inherit', env: { ...process.env, NEXTGO_DEMO_BASE_PATH: variant.base, NEXTGO_DEMO_ASSET_PREFIX: variant.assets, NEXTGO_DEMO_TRAILING_SLASH: String(variant.slash) },
}));
const server = createServer((incoming, outgoing) => {
  const url = new URL(incoming.url, 'http://127.0.0.1:4107');
  if (url.pathname.startsWith('/_e2e/activate/')) {
    const mode = url.pathname.slice('/_e2e/activate/'.length);
    if (incoming.method !== 'POST' || !Object.hasOwn(variants, mode)) { outgoing.writeHead(400).end(); return; }
    active = mode; outgoing.writeHead(204).end(); return;
  }
  if (url.pathname === '/_e2e/status') { outgoing.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ active })); return; }
  const variant = variants[active];
  if (active === 'moved' && !url.pathname.startsWith('/new/') && url.pathname !== '/new' && !url.pathname.startsWith('/new-cdn/')) {
    const local = url.pathname === '/old' ? '/' : url.pathname.startsWith('/old/') ? url.pathname.slice('/old'.length) : url.pathname;
    outgoing.writeHead(308, { Location: `/new${local}${url.search}` }).end(); return;
  }
  const upstream = request({ hostname: '127.0.0.1', port: variant.port, method: incoming.method, path: incoming.url, headers: { ...incoming.headers, host: '127.0.0.1:4107' } }, response => {
    outgoing.writeHead(response.statusCode, response.headers); response.pipe(outgoing);
  });
  upstream.on('error', () => { if (!outgoing.headersSent) outgoing.writeHead(502); outgoing.end(); });
  outgoing.on('close', () => upstream.destroy());
  incoming.pipe(upstream);
});
function close(code = 0) {
  if (closing) return;
  closing = true;
  for (const child of children) child.kill('SIGTERM');
  server.close();
  Promise.all(children.map(child => child.exitCode !== null || child.signalCode !== null ? undefined : new Promise(resolve => child.once('exit', resolve)))).then(() => process.exit(code));
}
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => close());
for (const child of children) { child.on('error', error => { console.error(error); close(1); }); child.on('exit', () => { if (!closing) close(1); }); }
for (const variant of Object.values(variants)) {
  let ready = false;
  for (let attempt = 0; attempt < 150; attempt++) {
    try { ready = (await fetch(`http://127.0.0.1:${variant.port}${variant.base}/api/health${variant.slash ? '/' : ''}`)).ok; } catch {}
    if (ready) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  if (!ready) { close(1); throw new Error(`instance ${variant.port} did not start`); }
}
server.listen(4107, '127.0.0.1');
