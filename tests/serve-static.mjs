import { spawn } from 'node:child_process';
import { mkdir, readFile, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, relative, resolve } from 'node:path';

const root = process.env.NEXTGO_STATIC_E2E_ROOT;
const output = join(root, 'static');
const mountedOutput = join(root, 'mounted-static');
const localeOutput = join(root, 'locale-static');
const defaultLocaleOutput = join(root, 'default-locale-static');
await mkdir(root, { recursive: true });
let child;
const servers = [];
for (const signal of ['SIGTERM', 'SIGINT']) {
  process.on(signal, () => {
    child?.kill(signal);
    if (servers.length) Promise.all(servers.map(server => new Promise(resolve => server.close(resolve)))).then(() => process.exit(0));
  });
}

async function run(command, args, environment = {}) {
  child = spawn(command, args, { stdio: 'inherit', env: { ...process.env, ...environment } });
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', resolve);
  });
  child = null;
  if (code !== 0) throw new Error(`${command} exited with ${code}`);
}

await run('go', ['build', '-o', join(root, 'nextgo'), './cmd/nextgo']);
await run(join(root, 'nextgo'), ['export', '-out', output, '-paths', '/,/about,/posts,/posts/html-first,/welcome,/docs/guide,/environment,/lazy,/lazy/details,/scripts,/scripts/details,/market/coffee,/market/coffee/espresso,/market/coffee/summary,/keyed,/keyed/details,/keyed/table,/composition,/composition/details,/field-preview,/choice-preview,/choice-pair,/file-preview', './examples/demo']);
await run(join(root, 'nextgo'), ['export', '-out', mountedOutput, '-paths', '/,/about,/posts,/posts/html-first', './examples/demo'], { NEXTGO_DEMO_BASE_PATH: '/mounted-static', NEXTGO_DEMO_ASSET_PREFIX: '/mounted-cdn', NEXTGO_DEMO_TRAILING_SLASH: 'true', NEXTGO_DEMO_VIEW_TRANSITIONS: 'true' });
const localeEnvironment = { NEXTGO_DEMO_LOCALES: 'true', NEXTGO_DEMO_LOCALE_DETECT: 'false', NEXTGO_DEMO_LOCALE_DOMAINS: '' };
await run(join(root, 'nextgo'), ['export', '-out', localeOutput, '-paths', '/translations,/about,/posts/html-first', './examples/demo'], { ...localeEnvironment, NEXTGO_DEMO_BASE_PATH: '/locale-static', NEXTGO_DEMO_ASSET_PREFIX: '/locale-assets' });
await run(join(root, 'nextgo'), ['export', '-out', defaultLocaleOutput, '-paths', '/translations,/about,/posts/html-first', './examples/demo'], { ...localeEnvironment, NEXTGO_DEMO_BASE_PATH: '/default-locale-static', NEXTGO_DEMO_ASSET_PREFIX: '/default-locale-assets', NEXTGO_DEMO_LOCALE_PREFIX_DEFAULT: 'false', NEXTGO_DEMO_TRAILING_SLASH: 'true' });

const domainOutputs = new Map();
for (const [port, code] of [[4124, 'en'], [4125, 'fr']]) {
  const directory = join(root, `domain-static-${code}`);
  await run(join(root, 'nextgo'), ['export', '-domain', `127.0.0.1:${port}`, '-out', directory, '-paths', '/translations,/about,/posts/html-first', './examples/demo'], {
    ...localeEnvironment, NEXTGO_DEMO_BASE_PATH: '/domain-static', NEXTGO_DEMO_ASSET_PREFIX: '/domain-assets', NEXTGO_DEMO_TRAILING_SLASH: 'true',
    NEXTGO_DEMO_LOCALE_DOMAINS: '127.0.0.1:4124=en,127.0.0.1:4125=fr',
  });
  const manifest = JSON.parse(await readFile(join(directory, '.nextgo-export.json'), 'utf8'));
  if (manifest.domain !== `127.0.0.1:${port}`) throw new Error('export selected a different domain');
  domainOutputs.set(port, { directory, domain: manifest.domain });
}

// No Go application server remains. Only generated files are served here.
const types = {
  '.html': 'text/html; charset=utf-8',
  '.json': 'application/json',
  '.wasm': 'application/wasm',
  '.js': 'application/javascript',
  '.css': 'text/css',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.svg': 'image/svg+xml',
};
const files = (selectDirectory, domain) => async (request, response) => {
  // Static hosts must apply the artifact's domain policy explicitly.
  if (domain && request.headers.host !== domain) { response.writeHead(421).end(); return; }
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    response.writeHead(405).end();
    return;
  }
  try {
    const pathname = decodeURIComponent(new URL(request.url, 'http://localhost').pathname);
    const directory = selectDirectory(pathname);
    const target = resolve(directory, `.${pathname}`);
    const rel = relative(directory, target);
    if (rel.startsWith('..') || rel.startsWith('/')) {
      response.writeHead(400).end();
      return;
    }
    let file = target;
    if ((await stat(file)).isDirectory()) file = join(file, 'index.html');
    const content = await readFile(file);
    response.writeHead(200, {
      'Content-Type': types[extname(file)] || 'application/octet-stream',
      'Content-Length': content.length,
      'Cache-Control': 'no-store',
    });
    response.end(request.method === 'HEAD' ? undefined : content);
  } catch (error) {
    response.writeHead(error instanceof URIError ? 400 : 404).end();
  }
};
const primary = createServer(files(pathname => {
  const mounted = prefix => pathname === prefix || pathname.startsWith(prefix + '/');
  return mounted('/locale-static') || mounted('/locale-assets') ? localeOutput : mounted('/default-locale-static') || mounted('/default-locale-assets') ? defaultLocaleOutput : mounted('/mounted-static') || mounted('/mounted-cdn') ? mountedOutput : output;
}));
servers.push(primary);
for (const [port, { directory, domain }] of domainOutputs) {
  const server = createServer(files(() => directory, domain));
  servers.push(server);
  await new Promise(resolve => server.listen(port, '127.0.0.1', resolve));
}
primary.listen(4102, '127.0.0.1', () => console.log('Static-only example: http://127.0.0.1:4102'));
