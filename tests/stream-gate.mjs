import { Agent, createServer, request as requestHTTP } from 'node:http';

// Forward actual server bytes. Only the selected first document is gated;
// styles and compiler-matched runtime assets remain ordinary HTTP responses.
export async function createStreamGate(upstreamOrigin, pathname = '/stream') {
  const upstream = new URL(upstreamOrigin);
  const agent = new Agent({ keepAlive: false });
  const sockets = new Set(), requests = new Set();
  let claimed = false, released = false, resume, closing;
  let shellResolve, shellReject;
  const shell = new Promise((resolve, reject) => { shellResolve = resolve; shellReject = reject; });
  // A navigation can fail before its caller reaches await shell.
  shell.catch(() => {});
  const server = createServer((request, response) => {
    const target = new URL(request.url, upstreamOrigin);
    const gate = !claimed && request.method === 'GET' && target.pathname === pathname && !request.headers['x-nextgo-navigate'];
    if (gate) claimed = true;
    const outgoing = requestHTTP({ agent, hostname: upstream.hostname, port: upstream.port, method: request.method, path: request.url, headers: { ...request.headers, 'accept-encoding': 'identity' } }, incoming => {
      const headers = { ...incoming.headers };
      for (const name of ['content-length', 'connection', 'keep-alive', 'transfer-encoding']) delete headers[name];
      incoming.on('error', error => { if (gate) shellReject(error); response.destroy(); });
      if (!gate) { response.writeHead(incoming.statusCode, headers); incoming.pipe(response); return; }
      if (incoming.statusCode !== 200 || !String(headers['content-type']).includes('text/html') || headers['content-encoding']) {
        shellReject(new Error('stream gate requires genuine uncompressed HTML200'));
        response.writeHead(incoming.statusCode, headers); incoming.pipe(response); return;
      }
      let prefix = Buffer.alloc(0), held = Buffer.alloc(0), ready = false;
      const collect = chunk => {
        if (prefix.length + chunk.length > 2 * 1024 * 1024) {
          shellReject(new Error('stream gate initial prefix exceeds 2 MiB'));
          incoming.destroy(); response.destroy(); return;
        }
        prefix = Buffer.concat([prefix, chunk]);
        const text = prefix.toString('utf8');
        const marker = text.indexOf('<span id="nextgo-initial-ready"');
        const boot = marker < 0 ? null : /<script\b[^>]*\bsrc="[^"]*\/boot\.js"[^>]*><\/script>/.exec(text.slice(marker));
        if (!boot) return;
        const cut = Buffer.byteLength(text.slice(0, marker + boot.index + boot[0].length));
        if (!text.slice(marker, marker + boot.index).includes('/wasm_exec.js')) {
          shellReject(new Error('stream gate bootstrap lacks matching runtime support'));
          incoming.destroy(); response.destroy(); return;
        }
        ready = true;
        incoming.pause(); incoming.removeListener('data', collect);
        // Keep at most the current chunk's remainder; do not collect later
        // deferred bytes. IncomingMessage/pipe backpressure owns their buffers.
        held = Buffer.from(prefix.subarray(cut));
        response.writeHead(incoming.statusCode, headers);
        response.write(prefix.subarray(0, cut)); prefix = undefined;
        resume = () => {
          response.write(held); held = undefined;
          if (incoming.readableEnded) response.end(); else incoming.pipe(response);
          incoming.resume();
        };
        shellResolve({ bytes: cut });
        if (released) { const start = resume; resume = undefined; start(); }
      };
      incoming.on('data', collect);
      incoming.on('end', () => { if (!ready) { shellReject(new Error('stream ended before complete initial shell/bootstrap')); response.destroy(); } });
    });
    requests.add(outgoing);
    outgoing.on('close', () => requests.delete(outgoing));
    outgoing.on('error', error => {
      if (gate) shellReject(error);
      response.destroy();
    });
    request.on('aborted', () => outgoing.destroy());
    response.on('close', () => { if (!response.writableEnded) outgoing.destroy(); });
    request.pipe(outgoing);
  });
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  return {
    origin: `http://127.0.0.1:${server.address().port}`,
    shell,
    release() {
      if (released) return;
      released = true;
      if (resume) { const start = resume; resume = undefined; start(); }
    },
    close() {
      if (!closing) {
        shellReject(new Error('stream gate closed'));
        closing = new Promise(resolve => server.close(resolve));
        for (const outgoing of requests) outgoing.destroy();
        agent.destroy();
        server.closeAllConnections();
        for (const socket of sockets) socket.destroy();
      }
      return closing;
    },
  };
}
