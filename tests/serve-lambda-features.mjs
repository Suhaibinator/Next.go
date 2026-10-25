import {createHash} from 'node:crypto';
import {createReadStream} from 'node:fs';
import {resolve} from 'node:path';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

const binary = process.env.NEXTGO_E2E_SERVER_BINARY || process.env.NEXTGO_TEST_BINARY;
if (!binary) throw new Error('Set NEXTGO_E2E_SERVER_BINARY or NEXTGO_TEST_BINARY to the existing production demo executable; this fixture never builds Go.');
const path = resolve(binary), hash = createHash('sha256');
for await (const chunk of createReadStream(path)) hash.update(chunk);
console.log(`Lambda browser fixture prebuilt=${path} sha256=${hash.digest('hex')}`);
const workers = [];
let closing;
async function close() {
  closing ||= Promise.allSettled(workers.map(worker => worker.close())).then(results => {
    for (const result of results) if (result.status === 'rejected') {console.error(result.reason); process.exitCode = 1;}
  });
  return closing;
}
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {void close();});
try {
  workers.push(await startLambdaGateway({binary: path, mode: 'buffered', port: 4196}));
  workers.push(await startLambdaGateway({binary: path, mode: 'streaming', port: 4197}));
} catch (error) {await close(); throw error;}
