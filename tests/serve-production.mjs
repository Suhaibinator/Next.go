import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { readerToken, adminToken } from './access-fixture.mjs';

const output = join(process.env.NEXTGO_E2E_ROOT, 'production');
await mkdir(output, { recursive: true });
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit' });
  if (await new Promise(resolve => build.on('exit', resolve)) !== 0) process.exit(1);
}
// Both cwd and the executable are outside the repository. No source assets exist here.
const server = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4100'], { cwd: output, stdio: 'inherit', env: { ...process.env, NEXTGO_DEMO_READER_TOKEN: readerToken, NEXTGO_DEMO_ADMIN_TOKEN: adminToken } });
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
