import { spawn } from 'node:child_process';
import { cp, mkdir, readdir } from 'node:fs/promises';
import { join } from 'node:path';

// Rebuild tests edit an isolated copy, never the working application's source.
const root = join(process.env.NEXTGO_E2E_ROOT, 'development');
await mkdir(root, { recursive: true });
const entries = await readdir('.', { withFileTypes: true });
const omitted = new Set(['node_modules', 'dist', 'docs', 'tests', 'test-results', 'test-results-crossbrowser', 'playwright-report']);
const frameworkSources = entries.filter(entry => entry.isFile() && (
  (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) ||
  entry.name === 'go.mod' || entry.name === 'go.sum'
)).map(entry => entry.name);
const frameworkFolders = entries.filter(entry => entry.isDirectory() && !entry.name.startsWith('.') && !omitted.has(entry.name)).map(entry => entry.name);
for (const item of [...frameworkSources, ...frameworkFolders]) {
  await cp(item, join(root, item), { recursive: true, filter: path => !path.split('/').includes('.nextgo') });
}
const compile = spawn('go', ['build', '-o', join(root, 'nextgo'), './cmd/nextgo'], { cwd: root, stdio: 'inherit' });
if (await new Promise(resolve => compile.on('exit', resolve)) !== 0) process.exit(1);
const server = spawn(join(root, 'nextgo'), ['dev', '-addr', '127.0.0.1:4101', './examples/demo'], { cwd: root, stdio: 'inherit' });
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.kill(signal));
server.on('exit', code => process.exit(code ?? 0));
