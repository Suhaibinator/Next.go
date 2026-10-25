// File-only hosting for an already exported metadata fixture. It neither builds
// nor routes application requests, and never falls back to an SPA document.
import {createServer} from 'node:http';
import {open, realpath, stat} from 'node:fs/promises';
import {constants} from 'node:fs';
import {extname, isAbsolute, join, relative, resolve, sep} from 'node:path';

const maxFileBytes = 32 * 1024 * 1024;
const types = {
  '.html': 'text/html; charset=utf-8', '.txt': 'text/plain; charset=utf-8',
  '.xml': 'application/xml; charset=utf-8', '.json': 'application/json',
  '.webmanifest': 'application/manifest+json', '.ico': 'image/x-icon',
  '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg',
  '.gif': 'image/gif', '.webp': 'image/webp', '.avif': 'image/avif',
  '.svg': 'image/svg+xml', '.css': 'text/css; charset=utf-8',
  '.js': 'application/javascript', '.mjs': 'application/javascript',
  '.wasm': 'application/wasm', '.pdf': 'application/pdf',
  '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf', '.otf': 'font/otf',
  '.mp3': 'audio/mpeg', '.wav': 'audio/wav', '.ogg': 'audio/ogg',
  '.mp4': 'video/mp4', '.webm': 'video/webm', '.vtt': 'text/vtt; charset=utf-8',
};
function contained(root, path) {
  const local = relative(root, path);
  return local !== '..' && !local.startsWith('..'+sep) && !isAbsolute(local);
}
function fail(response, status) {
  if (!response.headersSent) response.writeHead(status, {'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff'});
  response.end();
}

export async function startMetadataStaticServer(directory, port = 4207) {
  const root = await realpath(resolve(directory));
  if (!(await stat(root)).isDirectory()) throw new Error('NEXTGO_METADATA_STATIC_ROOT must be an exported directory.');
  const server = createServer(async (request, response) => {
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      response.setHeader('Allow', 'GET, HEAD'); fail(response, 405); return;
    }
    let file;
    try {
      const pathname = decodeURIComponent(new URL(request.url, 'http://localhost').pathname);
      if (!pathname.startsWith('/') || pathname.includes('\\') || pathname.includes('\0')) {fail(response, 400); return;}
      let target = resolve(root, '.'+pathname);
      if (!contained(root, target)) {fail(response, 400); return;}
      target = await realpath(target);
      if (!contained(root, target)) {fail(response, 404); return;}
      const targetInfo = await stat(target);
      if (targetInfo.isDirectory()) {
        target = await realpath(join(target, 'index.html'));
        if (!contained(root, target)) {fail(response, 404); return;}
      } else if (pathname.endsWith('/')) {fail(response, 404); return;}
      // Open the already resolved canonical filename without following a final
      // symlink. No named symlink may expose a target outside the export root.
      file = await open(target, constants.O_RDONLY | constants.O_NOFOLLOW);
      const info = await file.stat();
      if (!info.isFile()) {fail(response, 404); return;}
      if (!Number.isSafeInteger(info.size) || info.size < 0 || info.size > maxFileBytes) {fail(response, 413); return;}
      let body;
      if (request.method === 'GET') {
        // The extra byte detects growth without an unbounded readFile allocation.
        const bounded = Buffer.alloc(info.size+1);
        let length = 0;
        while (length < bounded.length) {
          const {bytesRead} = await file.read(bounded, length, bounded.length-length, length);
          if (bytesRead === 0) break;
          length += bytesRead;
        }
        if (length !== info.size || (await file.stat()).size !== info.size) {fail(response, 409); return;}
        body = bounded.subarray(0, length);
      }
      response.writeHead(200, {
        'Content-Type': types[extname(target).toLowerCase()] || 'application/octet-stream',
        'Content-Length': info.size, 'Cache-Control': 'no-cache',
        'X-Content-Type-Options': 'nosniff',
      });
      response.end(body);
    } catch (error) {
      fail(response, error instanceof URIError ? 400 : 404);
    } finally {await file?.close();}
  });
  await new Promise((done, reject) => {server.once('error', reject); server.listen(port, '127.0.0.1', done);});
  return server;
}

export function closeMetadataHTTPServer(server) {
  if (!server) return;
  server.close();
  server.closeIdleConnections();
  const timer = setTimeout(() => server.closeAllConnections(), 1000);
  timer.unref();
  server.once('close', () => clearTimeout(timer));
}
