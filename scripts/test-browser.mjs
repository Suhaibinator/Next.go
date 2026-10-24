// Own the disposable fixture workspaces until Playwright and its servers exit.
// Explicitly supplied roots and persistent test reports remain caller-owned.
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

const owned = [];
try {
  const env = { ...process.env };
  for (const [key, prefix] of [['NEXTGO_E2E_ROOT', 'nextgo-e2e-'], ['NEXTGO_STATIC_E2E_ROOT', 'nextgo-static-e2e-']]) {
    if (!env[key]) {
      env[key] = await mkdtemp(join(tmpdir(), prefix));
      owned.push(env[key]);
    }
  }
  const cli = fileURLToPath(new URL('../node_modules/@playwright/test/cli.js', import.meta.url));
  const child = spawn(process.execPath, [cli, 'test', ...process.argv.slice(2)], { stdio: 'inherit', env });
  const interrupt = () => child.kill('SIGINT');
  const terminate = () => child.kill('SIGTERM');
  process.on('SIGINT', interrupt);
  process.on('SIGTERM', terminate);
  try {
    process.exitCode = await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('exit', code => resolve(code ?? 1));
    });
  } finally {
    process.off('SIGINT', interrupt);
    process.off('SIGTERM', terminate);
  }
} finally {
  await Promise.all(owned.map(path => rm(path, { recursive: true, force: true })));
}
