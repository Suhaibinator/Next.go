// A real HTTP redirect is required: Playwright WebKit cannot fulfill an
// intercepted request with a synthetic redirect status. Other traffic reaches
// the same production Go artifact used by the runtime suite.
import { createServer, request } from 'node:http';

let forwarded = 0;
const receiver = createServer((req, res) => {
  // Permit a redirected JSON POST so a missing redirect:error policy cannot
  // accidentally pass merely because CORS blocked the foreign receiver.
  res.setHeader('Access-Control-Allow-Origin', req.headers.origin || '*');
  res.setHeader('Access-Control-Allow-Credentials', 'true');
  res.setHeader('Access-Control-Allow-Methods', 'POST, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', req.headers['access-control-request-headers'] || 'content-type, x-nextgo-action-protocol, x-nextgo-action-revision');
  res.setHeader('Vary', 'Origin');
  if (req.method === 'OPTIONS') { res.writeHead(204); res.end(); return; }
  if (req.method === 'POST') forwarded++;
  req.resume(); res.end('unexpected forwarded request');
});
const proxy = createServer((req, res) => {
  if (req.url === '/_e2e/health') { res.end('ready'); return; }
  if (req.url === '/_e2e/forwarded') {
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ forwarded })); return;
  }
  if (req.method === 'POST' && req.url === '/_nextgo/actions/optimistic-increment/v1') {
    req.resume(); res.writeHead(307, { Location: 'http://127.0.0.1:4133/typed-forward-probe' }); res.end(); return;
  }
  const upstream = request({ hostname: '127.0.0.1', port: 4100, path: req.url, method: req.method, headers: req.headers }, response => {
    res.writeHead(response.statusCode, response.headers); response.pipe(res);
  });
  upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
  req.on('aborted', () => upstream.destroy());
  res.on('close', () => { if (!res.writableEnded) upstream.destroy(); });
  req.pipe(upstream);
});
await Promise.all([new Promise(resolve => receiver.listen(4133, '127.0.0.1', resolve)), new Promise(resolve => proxy.listen(4132, '127.0.0.1', resolve))]);
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => {
  proxy.close(); receiver.close(); proxy.closeAllConnections(); receiver.closeAllConnections();
});
