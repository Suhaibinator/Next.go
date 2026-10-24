import { spawn } from 'node:child_process';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { createInterface } from 'node:readline';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(process.env.NEXTGO_E2E_ROOT, 'production', 'server');
const records = [];
const control = createServer((request, response) => {
  if (request.url !== '/_e2e/traces' || request.method !== 'GET') { response.writeHead(404).end(); return; }
  response.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }).end(JSON.stringify(records));
});
await new Promise(resolve => control.listen(4127, '127.0.0.1', resolve));
const child = spawn(binary, ['-addr', '127.0.0.1:4126'], {
  cwd: '/', stdio: ['ignore', 'ignore', 'pipe'], env: { ...process.env, NEXTGO_DEMO_TRACING: 'true', NEXTGO_DEMO_OTLP_ENDPOINT: '', NEXTGO_DEMO_TRACE_TRUST_INCOMING: 'false', NEXTGO_DEMO_LOCALES: 'false', NEXTGO_DEMO_BASE_PATH: '', NEXTGO_DEMO_ASSET_PREFIX: '', NEXTGO_DEMO_TRAILING_SLASH: 'false' },
});
createInterface({ input: child.stderr }).on('line', line => {
  if (line.length > 16384) return;
  try { const record = JSON.parse(line); if (record.spanId && record.traceId) { records.push(record); if (records.length > 1024) records.shift(); } } catch {}
});
let closing = false;
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => { closing = true; child.kill('SIGTERM'); control.close(); });
child.on('exit', code => { control.close(() => process.exit(closing ? 0 : code || 1)); });
