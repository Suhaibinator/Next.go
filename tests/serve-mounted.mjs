import { spawn } from 'node:child_process';
import { createServer, request } from 'node:http';
import { join } from 'node:path';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const assets = createServer((incoming, outgoing) => {
  if (!incoming.url.startsWith('/cdn/assets/') && !incoming.url.startsWith('/cdn/_nextgo/')) {
    outgoing.writeHead(404).end(); return;
  }
  const upstream = request({ hostname: '127.0.0.1', port: 4104, path: incoming.url, method: incoming.method, headers: { ...incoming.headers, host: '127.0.0.1:4104' } }, response => {
    outgoing.writeHead(response.statusCode, response.headers); response.pipe(outgoing);
  });
  upstream.on('error', () => outgoing.writeHead(502).end());
  incoming.pipe(upstream);
});
assets.listen(4105, '127.0.0.1');
const server = spawn(binary, ['-addr', '127.0.0.1:4104'], {
  cwd: '/', stdio: 'inherit',
  env: { ...process.env, NEXTGO_DEMO_BASE_PATH: '/demo', NEXTGO_DEMO_ASSET_PREFIX: 'http://127.0.0.1:4105/cdn' },
});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => { server.kill(signal); assets.close(); });
server.on('exit', code => { assets.close(); process.exit(code ?? 0); });
