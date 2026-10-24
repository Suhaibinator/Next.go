import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { token, cacheURL, firstURL, secondURL } from './shared-cache-fixture.mjs';

const output = join(process.env.NEXTGO_E2E_ROOT, 'shared-cache');
await mkdir(output, { recursive: true });
const children = new Set();
let stopping = false;
function stop() { stopping = true; for (const child of children) child.kill('SIGTERM'); }
process.on('SIGTERM', stop); process.on('SIGINT', stop);
async function build(args) {
  const child = spawn('go', args, { stdio: 'inherit' }); children.add(child);
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
  children.delete(child);
  if (code !== 0 || stopping) throw new Error(`shared-cache fixture build exited ${code}`);
}
if (!process.env.NEXTGO_E2E_SERVER_BINARY) await build(['run', './cmd/nextgo', 'build', '-out', output, './examples/demo']);
if (!process.env.NEXTGO_E2E_CACHE_BINARY) await build(['build', '-trimpath', '-o', join(output, 'nextgo-cache'), './cmd/nextgo-cache']);
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_')));
env.NEXTGO_CACHE_TOKEN = token; env.NEXTGO_DEMO_SHARED_ENDPOINT = cacheURL;
function start(binary, args) {
  const child = spawn(binary, args, { cwd: output, stdio: 'inherit', env }); children.add(child);
  child.once('error', error => { console.error(error); stop(); process.exitCode = 1; });
  child.once('exit', code => {
    children.delete(child);
    if (!stopping) { stop(); process.exitCode = code || 1; }
  });
}
start(process.env.NEXTGO_E2E_CACHE_BINARY || join(output, 'nextgo-cache'), ['-addr', new URL(cacheURL).host, '-namespace', 'demo']);
for (const url of [firstURL, secondURL]) start(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', new URL(url).host]);
