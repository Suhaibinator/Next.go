import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';

const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const output = join(process.env.NEXTGO_E2E_ROOT, 'social-image');
await mkdir(output, {recursive: true});
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], {stdio: 'inherit', env: base});
  if (await new Promise((resolve, reject) => {build.once('exit', resolve); build.once('error', reject);}) !== 0) process.exit(1);
}
const server = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4179'], {cwd: '/', stdio: 'inherit', env: {...base, NEXTGO_DEMO_BASE_PATH: '/social-cards', NEXTGO_DEMO_ASSET_PREFIX: '/card-assets', NEXTGO_DEMO_CSP: 'true'}});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
