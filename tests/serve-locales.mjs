import { spawn } from 'node:child_process';
import { join } from 'node:path';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const domains = '127.0.0.1:4121=en,127.0.0.1:4122=fr';
const variants = [
  { port: 4120, base: '/shop', domains: '' },
  { port: 4121, base: '/domain', domains },
  { port: 4122, base: '/domain', domains },
];
let closing = false;
const children = variants.map(v => spawn(binary, ['-addr', `127.0.0.1:${v.port}`], {
  cwd: '/', stdio: 'inherit', env: { ...process.env, NEXTGO_DEMO_BASE_PATH: v.base,
    NEXTGO_DEMO_LOCALES: 'true', NEXTGO_DEMO_LOCALE_DOMAINS: v.domains,
    NEXTGO_DEMO_ASSET_PREFIX: '', NEXTGO_DEMO_TRAILING_SLASH: 'false' },
}));
async function close(code = 0) {
  if (closing) return; closing = true;
  for (const child of children) child.kill('SIGTERM');
  await Promise.all(children.map(child => child.exitCode !== null || child.signalCode !== null ? undefined : new Promise(resolve => child.once('exit', resolve))));
  process.exit(code);
}
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => close());
for (const child of children) child.on('exit', code => { if (!closing) close(code || 1); });
