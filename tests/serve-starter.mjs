import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const framework = fileURLToPath(new URL('..', import.meta.url));
const application = join(process.env.NEXTGO_E2E_ROOT, 'starter-app');
const output = join(process.env.NEXTGO_E2E_ROOT, 'starter-production');
await mkdir(output, { recursive: true });
async function run(args) {
  const child = spawn('go', args, { cwd: framework, stdio: 'inherit' });
  if (await new Promise(resolve => child.on('exit', resolve)) !== 0) process.exit(1);
}
await run(['run', './cmd/nextgo', 'init', '-module', 'starter.example/application', '-framework', framework, application]);
await run(['run', './cmd/nextgo', 'build', '-out', output, application]);
// The generated application and both source checkouts are absent from cwd.
const server = spawn(join(output, 'server'), ['-addr', '127.0.0.1:4103'], { cwd: output, stdio: 'inherit' });
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
