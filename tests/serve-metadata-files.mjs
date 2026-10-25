// Real prebuilt example binary and asset-only forwarding origin; no build or
// source mutation is allowed in this fixture.
import {spawn} from 'node:child_process';
import {createServer, request} from 'node:http';
import {access} from 'node:fs/promises';
import {constants} from 'node:fs';
import {resolve} from 'node:path';
import {startMetadataStaticServer, closeMetadataHTTPServer} from './metadata-static-server.mjs';

const configured = process.env.NEXTGO_METADATA_SERVER_BINARY || process.env.NEXTGO_E2E_SERVER_BINARY;
if (!configured) throw new Error('Set NEXTGO_METADATA_SERVER_BINARY to a prebuilt examples/metadata-site server binary. This fixture never builds.');
const binary = resolve(configured);
await access(binary, constants.X_OK);
const clean = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_METADATA_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const staticFiles = process.env.NEXTGO_METADATA_STATIC_ROOT ? await startMetadataStaticServer(process.env.NEXTGO_METADATA_STATIC_ROOT) : undefined;
let stopping = false;
const assets = createServer((incoming, outgoing) => {
  if (!incoming.url.startsWith('/metadata-assets/assets/') && !incoming.url.startsWith('/metadata-assets/_nextgo/')) {
    outgoing.writeHead(404, {'Cache-Control': 'no-store'}).end(); return;
  }
  const upstream = request({hostname: '127.0.0.1', port: 4205, path: incoming.url, method: incoming.method, headers: {...incoming.headers, host: '127.0.0.1:4205'}}, response => {
    outgoing.writeHead(response.statusCode, response.headers); response.pipe(outgoing);
  });
  upstream.on('error', () => {if (!outgoing.headersSent) outgoing.writeHead(502); outgoing.end();});
  outgoing.on('close', () => {if (!outgoing.writableEnded) upstream.destroy();});
  incoming.pipe(upstream);
});
await new Promise((done, reject) => {assets.once('error', reject); assets.listen(4206, '127.0.0.1', done);});
const server = spawn(binary, ['-addr', '127.0.0.1:4205'], {
  cwd: '/', stdio: 'inherit',
  env: {...clean, NEXTGO_METADATA_BASE_PATH: '/metadata-site', NEXTGO_METADATA_ASSET_PREFIX: 'http://127.0.0.1:4206/metadata-assets'},
});
function stop(code = 0) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  closeMetadataHTTPServer(assets);
  closeMetadataHTTPServer(staticFiles);
  server.kill('SIGTERM');
}
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => stop());
server.once('error', () => stop(1));
server.once('exit', code => {if (!stopping) stop(code || 1);});
