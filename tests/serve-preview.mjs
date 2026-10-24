import { spawn } from 'node:child_process';
import { createServer, request } from 'node:http';
import { join } from 'node:path';
import { adminToken, readerToken } from './access-fixture.mjs';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
let closing = false;
const assets = createServer((incoming, outgoing) => {
  if (!incoming.url.startsWith('/cdn/assets/') && !incoming.url.startsWith('/cdn/_nextgo/')) { outgoing.writeHead(404).end(); return; }
  // Exercise the framework's explicit public asset-only mount, not a page
  // server that happens to expose runtime files without public CORS headers.
  const upstream = request({ hostname: '127.0.0.1', port: 4136, path: incoming.url, method: incoming.method,
    headers: { host: '127.0.0.1:4136', 'accept-encoding': incoming.headers['accept-encoding'] || 'identity' } }, response => {
    outgoing.writeHead(response.statusCode, response.headers); response.pipe(outgoing);
  });
  upstream.on('error', () => { outgoing.writeHead(502); outgoing.end(); });
  incoming.pipe(upstream);
});
await new Promise(resolve => assets.listen(4137, '127.0.0.1', resolve));
const children = [
  { port: 4134, base: '', locales: 'false' },
  { port: 4135, base: '/shop', locales: 'true' },
  { port: 4136, base: '', locales: 'false', assets: 'http://127.0.0.1:4137/cdn' },
].map(v => spawn(binary, ['-addr', `127.0.0.1:${v.port}`], {
  cwd: '/', stdio: 'inherit', env: { ...process.env,
    NEXTGO_DEMO_DRAFT_KEY: 'd7'.repeat(32), NEXTGO_DEMO_DRAFT_HTTP: 'true',
    NEXTGO_DEMO_ADMIN_TOKEN: adminToken, NEXTGO_DEMO_READER_TOKEN: readerToken,
    NEXTGO_DEMO_BASE_PATH: v.base, NEXTGO_DEMO_LOCALES: v.locales,
    NEXTGO_DEMO_LOCALE_DOMAINS: '', NEXTGO_DEMO_LOCALE_DETECT: 'false',
    NEXTGO_DEMO_LOCALE_PREFIX_DEFAULT: 'true', NEXTGO_DEMO_ASSET_PREFIX: v.assets || '',
    NEXTGO_DEMO_TRAILING_SLASH: 'false' },
}));
async function close(code = 0) {
  if (closing) return; closing = true;
  assets.close(); assets.closeAllConnections();
  for (const child of children) child.kill('SIGTERM');
  await Promise.all(children.map(child => child.exitCode !== null || child.signalCode !== null ? undefined : new Promise(resolve => child.once('exit', resolve))));
  process.exit(code);
}
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => close());
for (const child of children) child.on('exit', code => { if (!closing) close(code || 1); });
