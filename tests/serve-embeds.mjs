import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';

const output = join(process.env.NEXTGO_E2E_ROOT, 'embeds');
await mkdir(output, {recursive: true});
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], {stdio: 'inherit', env: base});
  if (await new Promise(resolve => build.once('exit', resolve)) !== 0) process.exit(1);
}
const server = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4178'], {cwd: '/', stdio: 'inherit', env: {...base, NEXTGO_DEMO_BASE_PATH: '/safe-embeds', NEXTGO_DEMO_ASSET_PREFIX: '/embed-assets', NEXTGO_DEMO_CSP: 'true'}});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
