// Adapter-only unit tests. The temporary Node worker echoes actual Runtime API
// events; this is not application or cloud qualification.
import assert from 'node:assert/strict';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import {request as httpRequest} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

const port = 4233;
const workerSource = `#!/usr/bin/env node
let stopped=false, sequence=0;
const controller=new AbortController();
process.on('SIGTERM',()=>{stopped=true;controller.abort();});
const origin='http://'+process.env.AWS_LAMBDA_RUNTIME_API;
while(!stopped) {
 try {
  const next=await fetch(origin+'/2018-06-01/runtime/invocation/next',{signal:controller.signal});
  const event=await next.json();
  const body=JSON.stringify({sequence:++sequence,host:event.headers.host,domain:event.requestContext.domainName,proto:event.headers['x-forwarded-proto'],forwardedHost:event.headers['x-forwarded-host'],path:event.rawPath});
  const metadata={statusCode:200,headers:{'Content-Type':'application/json','X-Zones-Adapter-Probe':'actual-runtime-event'}};
  const mode=process.env.NEXTGO_LAMBDA_RESPONSE_MODE;
  const payload=mode==='streaming'?Buffer.concat([Buffer.from(JSON.stringify(metadata)),Buffer.alloc(8),Buffer.from(body)]):JSON.stringify({...metadata,body,isBase64Encoded:false});
  const response=await fetch(origin+'/2018-06-01/runtime/invocation/'+next.headers.get('Lambda-Runtime-Aws-Request-Id')+'/response',{method:'POST',headers:{'Lambda-Runtime-Function-Response-Mode':mode},body:payload,signal:controller.signal});
  if(response.status!==202) throw new Error('response rejected');
 } catch(error) {if(!stopped) throw error;}
}
`;

function request(host, extra = {}) {
  return new Promise((resolve, reject) => {
    const outgoing = httpRequest({hostname: '127.0.0.1', port, path: '/probe', headers: {Host: host, ...extra}, timeout: 3000}, response => {
      const chunks = []; let size = 0;
      response.on('data', chunk => {size += chunk.length; if (size > 4096) response.destroy(new Error('adapter probe response bound')); else chunks.push(chunk);});
      response.once('error', reject);
      response.once('end', () => resolve({status: response.statusCode, headers: response.headers, text: Buffer.concat(chunks, size).toString()}));
    });
    outgoing.once('error', reject); outgoing.once('timeout', () => outgoing.destroy(new Error('adapter probe timeout'))); outgoing.end();
  });
}

async function fixture(t, mode, allowedHosts) {
  const directory = await mkdtemp(join(tmpdir(), 'nextgo-zone-adapter-'));
  const binary = join(directory, 'worker.mjs');
  await writeFile(binary, workerSource, {mode: 0o600});
  t.after(() => rm(directory, {recursive: true, force: true}));
  const workerArgs = [binary];
  const worker = await startLambdaGateway({binary: process.execPath, workerArgs, mode, port, allowedHosts});
  workerArgs[0] = '/not-the-original-worker';
  t.after(() => worker.close());
}

for (const mode of ['buffered', 'streaming']) {
  test(`zone adapter ${mode} keeps default loopback-only admission`, {timeout: 10_000}, async t => {
    await fixture(t, mode);
    const local = `127.0.0.1:${port}`;
    const first = await request(local, {'X-Forwarded-Proto': 'https', 'X-Forwarded-Host': 'alpha.example.test', Forwarded: 'host=alpha.example.test;proto=https'});
    assert.equal(first.status, 200); assert.equal(first.headers['x-zones-adapter-probe'], 'actual-runtime-event');
    assert.deepEqual(JSON.parse(first.text), {sequence: 1, host: local, domain: local, proto: 'http', forwardedHost: 'alpha.example.test', path: '/probe'});
    for (const host of ['alpha.example.test', 'unowned.example.test', `localhost:${port}`]) {
      const denied = await request(host);
      assert.equal(denied.status, 400); assert.equal(denied.headers['x-zones-adapter-probe'], undefined);
    }
    assert.equal(JSON.parse((await request(local)).text).sequence, 2, 'denied authorities never enter Runtime API');
  });

  test(`zone adapter ${mode} forwards only opted-in exact actual hosts`, {timeout: 10_000}, async t => {
    const hosts = ['alpha.example.test', 'beta.example.test', 'unowned.example.test'];
    await fixture(t, mode, hosts);
    hosts.push('injected.example.test');
    for (const [index, host] of ['alpha.example.test', 'beta.example.test', 'unowned.example.test'].entries()) {
      const reply = await request(host, {'X-Forwarded-Host': 'forged.example.test', 'X-Forwarded-Proto': 'https'});
      assert.equal(reply.status, 200); assert.equal(reply.headers['x-nextgo-fixture-lambda-mode'], mode);
      assert.deepEqual(JSON.parse(reply.text), {sequence: index + 1, host, domain: host, proto: 'http', forwardedHost: 'forged.example.test', path: '/probe'});
    }
    for (const host of [`127.0.0.1:${port}`, 'foreign.example.test', 'injected.example.test', 'Alpha.example.test', 'alpha.example.test:443']) {
      const denied = await request(host);
      assert.equal(denied.status, 400); assert.equal(denied.headers['x-zones-adapter-probe'], undefined);
    }
    assert.equal(JSON.parse((await request('alpha.example.test')).text).sequence, 4);
  });
}

test('zone adapter rejects malformed or excessive operator host sets before startup', async () => {
  const bad = [null, 'alpha.example.test', [], Array(1), Array(33).fill('alpha.example.test'), ['alpha.example.test', 'alpha.example.test'], ['*'], ['https://alpha.example.test'], ['alpha.example.test/path'], ['alpha.example.test@other'], ['alpha.example.test:0'], ['alpha.example.test:65536'], ['Alpha.example.test'], ['bad host'], ['a'.repeat(260)]];
  bad.push(Array.from({length: 32}, (_, index) => `${'a'.repeat(63)}.${'b'.repeat(63)}.${index}.test`));
  for (const allowedHosts of bad) await assert.rejects(startLambdaGateway({binary: '/not-started', mode: 'buffered', port, allowedHosts}), /invalid fixture allowedHosts/);
});

test('zone adapter rejects malformed or excessive literal worker argv before startup', async () => {
  for (const workerArgs of [null, 'script.mjs', Array(1), Array(17).fill('x'), ['bad\0arg'], [42], ['x'.repeat(4097)], ['é'.repeat(2049)]]) {
    await assert.rejects(startLambdaGateway({binary: '/not-started', mode: 'buffered', port, workerArgs}), /invalid fixture workerArgs/);
  }
});
