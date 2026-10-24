// Development-only transport fixture: preserve the real server HTML while
// forcing the HTML parser to yield inside a deferred template.
import { createServer, request as requestHTTP } from 'node:http';

const pending = new Map();
const origin = 'http://127.0.0.1:4160';
const server = createServer(async (request, response) => {
  const url = new URL(request.url, origin);
  if (url.pathname === '/_fixture/ready') { response.end('ready'); return; }
  if (url.pathname === '/_fixture/release' && request.method === 'POST') {
    const state = pending.get(url.searchParams.get('key'));
    if (!state) { response.writeHead(404); response.end('no pending parser response'); return; }
    if (state.phase === 0) { state.response.write(state.partial); state.phase = 1; }
    else { state.response.end(state.remainder); pending.delete(url.searchParams.get('key')); }
    response.end('released'); return;
  }
  const split = url.pathname === '/stream' && url.searchParams.has('parser-split');
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: 4100, path: split ? '/stream' : request.url, method: request.method, headers: { ...request.headers, host: new URL(origin).host, 'accept-encoding': 'identity' } }, incoming => {
    const headers = { ...incoming.headers }; delete headers['content-length'];
    if (!split) { response.writeHead(incoming.statusCode, headers); incoming.pipe(response); return; }
    const pieces = [];
    incoming.on('data', chunk => pieces.push(chunk));
    incoming.on('end', () => {
      const html = Buffer.concat(pieces).toString('utf8');
      const start = html.indexOf('<template data-nextgo-chunk=');
      const end = html.indexOf('</template>', start);
      const island = html.indexOf('data-nextgo-island="counter"', start);
      const mode = url.searchParams.get('parser-split');
      const stop = mode === 'before-completion' ? end + '</template>'.length : mode === 'inside-island' ? html.indexOf('<output', island) : html.lastIndexOf('<', island);
      if (start < 0 || end < 0 || island < 0 || stop <= start) { response.writeHead(500); response.end('unexpected real stream markup'); return; }
      const key = url.searchParams.get('key');
      if (!key || pending.has(key)) { response.writeHead(409); response.end('unique parser key required'); return; }
      pending.set(key, { response, partial: html.slice(start, stop), remainder: html.slice(stop), phase: 0 });
      response.on('close', () => pending.delete(key));
      response.writeHead(incoming.statusCode, { ...headers, 'cache-control': 'no-store' });
      response.write(html.slice(0, start));
    });
    incoming.on('error', () => response.destroy());
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('upstream unavailable'); });
  request.pipe(upstream); response.on('close', () => upstream.destroy());
});
server.listen(4160, '127.0.0.1');
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  for (const state of pending.values()) state.response.destroy();
  server.closeAllConnections(); server.close(() => process.exit(0));
});
