import { spawn } from 'node:child_process';
import { join } from 'node:path';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const server = spawn(binary, ['-addr', '127.0.0.1:4106'], {
  cwd: '/', stdio: 'inherit',
  env: { ...process.env, NEXTGO_DEMO_BASE_PATH: '/slash', NEXTGO_DEMO_TRAILING_SLASH: 'true', NEXTGO_DEMO_ASSET_PREFIX: 'http://127.0.0.1:4105/cdn' },
});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
