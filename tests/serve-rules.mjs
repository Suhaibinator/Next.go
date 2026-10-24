import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { join } from 'node:path';

// Real upstream ownership boundary; never a replacement browser navigation API.
const upstream = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://upstream.invalid');
  if (url.pathname === '/document') { response.writeHead(200, { 'Content-Type': 'text/html' }).end('<!doctype html><html lang="en"><title>Proxy upstream</title><h1>Proxied document</h1><a href="/policy/rules">Back to the application</a>'); return; }
  if (url.pathname === '/slow') {
    const timer = setTimeout(() => response.end('late upstream'), 1000);
    response.on('close', () => clearTimeout(timer)); return;
  }
  if (url.pathname === '/redirect') { response.writeHead(307, { Location: '/policy/rules/remote/document' }).end(); return; }
  const chunks = []; let length = 0;
  for await (const chunk of request) { length += chunk.length; if (length > 3 << 20) { response.writeHead(413).end(); return; } chunks.push(chunk); }
  response.writeHead(200, { 'Content-Type': 'application/json', 'X-Nextgo-Protocol': '3', 'X-Nextgo-Configuration': 'a'.repeat(64), 'Set-Cookie': 'upstream=blocked' }).end(JSON.stringify({ method: request.method, url: request.url, host: request.headers.host, headers: request.headers, body: Buffer.concat(chunks).toString() }));
});
await new Promise(resolve => upstream.listen(4130, '127.0.0.1', resolve));
const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const child = spawn(binary, ['-addr', '127.0.0.1:4129'], {
 cwd: '/', stdio: 'inherit', env: { ...process.env, NEXTGO_DEMO_RULES: 'true', NEXTGO_DEMO_PROXY_ORIGIN: 'http://127.0.0.1:4130', NEXTGO_DEMO_PROXY_TIMEOUT: '150ms',
 NEXTGO_DEMO_BASE_PATH: '/policy', NEXTGO_DEMO_ASSET_PREFIX: '', NEXTGO_DEMO_LOCALES: 'false', NEXTGO_DEMO_TRACING: 'false', NEXTGO_DEMO_OTLP_ENDPOINT: '', NEXTGO_DEMO_VIEW_TRANSITIONS: 'false', NEXTGO_DEMO_TRAILING_SLASH: 'false' },
});
for (const signal of ['SIGTERM','SIGINT']) process.on(signal, () => { child.kill(signal); upstream.close(); });
child.on('exit', code => upstream.close(() => process.exit(code ?? 0)));
