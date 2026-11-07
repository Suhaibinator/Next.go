import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {initializeOfflineCIArtifacts,collectOfflineCIArtifacts} from '../scripts/offline-ci-artifacts.mjs';
import {observeOfflineUpdateFailure,retireOfflineParentFault} from './offline-update-failure-observation.mjs';

const small={totalBytes:16384,fileBytes:2048,metadataBytes:8192,profileBytes:8192,manifestBytes:4096,files:64,scanEntries:128};
async function fixture(t){
  const parent=await fs.mkdtemp(join(tmpdir(),'offline-ci-unit-'));t.after(()=>fs.rm(parent,{recursive:true,force:true}));
  const root=join(parent,'nextgo-offline-ci.test');await fs.mkdir(root,{mode:0o700});
  const destination=join(parent,'offline-ci-failure');await initializeOfflineCIArtifacts({ownedRoot:root,destination});
  return {parent,root,destination};
}
async function put(root,name,data){const path=join(root,...name.split('/'));await fs.mkdir(join(path,'..'),{recursive:true});await fs.writeFile(path,data);return path;}
async function actualBytes(root){let bytes=0;for(const e of await fs.readdir(root,{withFileTypes:true})){const p=join(root,e.name);assert.equal(e.isSymbolicLink(),false);if(e.isDirectory())bytes+=await actualBytes(p);else{assert.equal(e.isFile(),true);bytes+=(await fs.stat(p)).size;}}return bytes;}
async function missing(path){await assert.rejects(fs.lstat(path),e=>e.code==='ENOENT');}
async function manifest(destination){return JSON.parse(await fs.readFile(join(destination,'collection.json'),'utf8'));}
async function observeOpens(fn){
  const original=fs.open,opened=[];fs.open=async function(path,...args){opened.push(String(path));return original.call(this,path,...args);};
  try{return {result:await fn(),opened};}finally{fs.open=original;}
}

test('success/default is a true no-op even for nonexistent foreign paths',async()=>{
  const result=await collectOfflineCIArtifacts({ownedRoot:'/definitely-not-a-ci-root'});assert.deepEqual(result,{status:'skipped-not-failure'});
});

test('foreign/nonempty roots and forged ownership are refused before output',async t=>{
  const parent=await fs.mkdtemp(join(tmpdir(),'offline-ci-foreign-'));t.after(()=>fs.rm(parent,{recursive:true,force:true}));
  const root=join(parent,'nextgo-offline-ci.foreign');await fs.mkdir(root,{mode:0o700});await put(root,'source.go','FOREIGN SOURCE');
  const destination=join(parent,'offline-ci-failure');
  await assert.rejects(initializeOfflineCIArtifacts({ownedRoot:root,destination}),/empty directory/);
  await assert.rejects(collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));await missing(destination);
  const good=await fixture(t);const marker=JSON.parse(await fs.readFile(join(good.root,'.nextgo-offline-ci-owner.json'),'utf8'));
  marker.root=root;await fs.chmod(join(good.root,'.nextgo-offline-ci-owner.json'),0o600);await fs.writeFile(join(good.root,'.nextgo-offline-ci-owner.json'),JSON.stringify(marker));
  await assert.rejects(collectOfflineCIArtifacts({failed:true,ownedRoot:good.root,limits:small}),/foreign ownership/);await missing(good.destination);
});

test('copies only allowlisted reports, logs and owned profile bytes; leaves originals intact',async t=>{
  const {root,destination}=await fixture(t);
  await put(root,'report/report.json','{"failed":true}');await put(root,'build/source-diff.json','{"differences":1}');
  await put(root,'build/logs/A-build.log','compiler diagnostic');
  const refused=['build/artifacts/A/server','build/sources/A/source.go','go-cache/cache','module-cache/cache','browser/case/attachments/source.go','browser/case/artifacts/server.json'];
  for(const name of refused)await put(root,name,'DO NOT COPY');
  const profile='browser/case/private-persistent-profile/Default/cache';await put(root,profile,'OWNED PROFILE');
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));assert.equal(result.status,'collected-failure-only');
  for(const name of refused)assert.ok(!opened.includes(join(root,name)),'forbidden payload was opened: '+name);
  assert.equal(await fs.readFile(join(destination,profile),'utf8'),'OWNED PROFILE');assert.equal(await fs.readFile(join(root,profile),'utf8'),'OWNED PROFILE');
  for(const name of ['build/artifacts','build/sources','go-cache','module-cache','browser/case/attachments/source.go','browser/case/artifacts'])await missing(join(destination,name));
  assert.equal(await actualBytes(destination),result.totalOutputBytes);assert.ok(result.totalOutputBytes<=small.totalBytes);
});

test('metadata per-file cap omits before opening, records omission and never copies bytes',async t=>{
  const {root,destination}=await fixture(t);const path=await put(root,'report/report.json',Buffer.alloc(2049,0x61));
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));
  assert.ok(!opened.includes(path));await missing(join(destination,'report/report.json'));
  assert.ok((await manifest(destination)).omitted.some(e=>e.path==='report/report.json'&&e.reason==='metadata-or-file-bound'));assert.equal(result.payloadBytes,0);
});

test('metadata total payload budget stops later source reads',async t=>{
  const {root,destination}=await fixture(t);await put(root,'report/report.json',Buffer.alloc(1500,0x61));const later=await put(root,'build/index.json',Buffer.alloc(1500,0x62));
  const limits={...small,totalBytes:8192,manifestBytes:6144};
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits}));
  assert.ok(!opened.includes(later));assert.equal(result.payloadBytes,1500);assert.ok(await actualBytes(destination)<=limits.totalBytes);
  assert.ok((await manifest(destination)).omitted.some(e=>e.path==='build/index.json'&&e.reason==='payload-or-file-bound'));
});

test('whole-profile byte refusal happens before ANY profile open',async t=>{
  const {root,destination}=await fixture(t);const first=await put(root,'browser/fail/private-persistent-profile/one',Buffer.alloc(600,0x61));const second=await put(root,'browser/fail/private-persistent-profile/two',Buffer.alloc(600,0x62));
  const limits={...small,profileBytes:1000};
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits}));
  assert.ok(!opened.includes(first)&&!opened.includes(second));assert.equal(result.copiedProfileBytes,0);
  await missing(join(destination,'browser/fail/private-persistent-profile'));assert.equal((await fs.stat(first)).size,600);
  assert.ok((await manifest(destination)).profiles.some(e=>e.status==='omitted'&&e.reason==='profile-byte-bound'));
});

test('one oversized profile file refuses the entire regular-file group without reads',async t=>{
  const {root}=await fixture(t);const one=await put(root,'browser/fail/private-persistent-profile/one','small');const big=await put(root,'browser/fail/private-persistent-profile/big',Buffer.alloc(2049));
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));
  assert.equal(result.copiedProfileBytes,0);assert.ok(!opened.includes(one)&&!opened.includes(big));assert.ok(result.profiles.some(e=>e.reason==='profile-file-or-scan-bound'));
});

test('total and file-count budgets do not open a later admitted-looking profile',async t=>{
  for(const limits of [{...small,totalBytes:8192,manifestBytes:6144},{...small,files:1}]){
    const f=await fixture(t);await put(f.root,'report/report.json',Buffer.alloc(1500));const profile=await put(f.root,'browser/fail/private-persistent-profile/db',Buffer.alloc(600));
    const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:f.root,limits}));assert.ok(!opened.includes(profile));assert.equal(result.copiedProfileBytes,0);assert.ok(result.profiles.some(e=>e.status==='omitted'));
  }
});

test('symlink files and ancestor symlinks never read or copy foreign bytes',async t=>{
  const {root,parent,destination}=await fixture(t);const foreign=await put(parent,'foreign/secret.txt','FOREIGN SECRET');
  await fs.mkdir(join(root,'browser'),{recursive:true});await fs.symlink(foreign,join(root,'browser','secret.txt'));
  await fs.symlink(join(parent,'foreign'),join(root,'build'));
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));
  assert.ok(!opened.includes(foreign));assert.equal(result.payloadBytes,0);assert.ok(result.omitted.some(e=>e.reason==='symlink-refused'));assert.ok(result.omitted.some(e=>e.reason==='unsafe-parent'));
  await missing(join(destination,'browser','secret.txt'));assert.equal(await fs.readFile(foreign,'utf8'),'FOREIGN SECRET');
});

test('FIFO special file is refused without opening or changing it',async t=>{
  const {root,destination}=await fixture(t);await fs.mkdir(join(root,'browser'),{recursive:true});const fifo=join(root,'browser','diagnostic.log');
  await promisify(execFile)('mkfifo',[fifo]);
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}));
  assert.ok(!opened.includes(fifo));assert.ok(result.omitted.some(e=>e.reason==='nonregular-refused'));assert.equal((await fs.lstat(fifo)).isFIFO(),true);await missing(join(destination,'browser','diagnostic.log'));
});

test('scan-bound never presents a partial discovered profile as complete',async t=>{
  const {root,destination}=await fixture(t);for(let i=0;i<20;i++)await put(root,'browser/fail/private-persistent-profile/f'+i,'a');
  const {result,opened}=await observeOpens(()=>collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:{...small,scanEntries:22}}));
  assert.equal(result.copiedProfileBytes,0);assert.ok(!opened.some(p=>p.includes('private-persistent-profile')));await missing(join(destination,'browser/fail/private-persistent-profile'));
  assert.ok(result.omitted.some(e=>e.reason==='scan-entry-limit'));
});

test('existing failure output is never overwritten or deleted',async t=>{
  const {root,destination}=await fixture(t);await fs.mkdir(destination);await put(destination,'keep.txt','last evidence');
  await assert.rejects(collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:small}),/destination exists/);assert.equal(await fs.readFile(join(destination,'keep.txt'),'utf8'),'last evidence');
});

test('limits cannot be enlarged beyond the fixed CI contract',async t=>{
  const {root,destination}=await fixture(t);await assert.rejects(collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:{totalBytes:1024<<20}}),/enlarged/);await missing(destination);
});


test('omission detail truncation retains a bounded manifest and aggregate inventory',async t=>{
  const {root,destination}=await fixture(t);
  for(let i=0;i<180;i++)await put(root,'browser/case/attachments/bad-'+i+'.go','not diagnostic');
  const result=await collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits:{...small,scanEntries:256}});
  const bytes=await fs.readFile(join(destination,'collection.json'));const saved=JSON.parse(bytes);
  assert.ok(bytes.length<=small.manifestBytes);assert.equal(saved.recordDetailsTruncated,true);
  assert.match(saved.inventorySHA256,/^[a-f0-9]{64}$/);assert.equal(result.payloadBytes,0);
  assert.ok(await actualBytes(destination)<=small.totalBytes);
});

test('initialization refuses overlapping output and an existing destination',async t=>{
  const parent=await fs.mkdtemp(join(tmpdir(),'offline-ci-init-'));t.after(()=>fs.rm(parent,{recursive:true,force:true}));
  const root=join(parent,'nextgo-offline-ci.test');await fs.mkdir(root,{mode:0o700});
  await assert.rejects(initializeOfflineCIArtifacts({ownedRoot:root,destination:join(root,'offline-ci-failure')}),/separate/);
  const destination=join(parent,'offline-ci-failure');await fs.mkdir(destination);await put(destination,'old.txt','preserve');
  await assert.rejects(initializeOfflineCIArtifacts({ownedRoot:root,destination}),/exists/);assert.equal(await fs.readFile(join(destination,'old.txt'),'utf8'),'preserve');
  assert.deepEqual(await fs.readdir(root),[]);
});


test('multi-chunk evidence is copied byte-exactly within its declared caps',async t=>{
  const {root,destination}=await fixture(t);const body=Buffer.from(JSON.stringify({payload:'a'.repeat(150000)}));await put(root,'report/report.json',body);
  const limits={...small,totalBytes:512<<10,fileBytes:256<<10,metadataBytes:256<<10};
  const result=await collectOfflineCIArtifacts({failed:true,ownedRoot:root,limits});
  assert.deepEqual(await fs.readFile(join(destination,'report/report.json')),body);
  assert.equal(result.copiedMetadataBytes,body.length);assert.equal(await actualBytes(destination),result.totalOutputBytes);
  assert.ok(result.totalOutputBytes<=limits.totalBytes);
});

// These prospective cases exercise the actual diagnostic helpers with plain
// synthetic objects. They do not start a browser, worker, process, timer or IO.
function offlineDOMObservationFixture(overrides = {}) {
  const prefix = 'nextgo: a conflicting service worker owns an overlapping scope';
  const attributes = new Map(Object.entries({
    'data-offline-last-error': `${prefix} [controller-changed]`,
    'data-offline-operation-error': 'conflict', 'data-offline-completed-operations': '2',
    'data-offline-controlling': 'true', 'data-offline-enabled': 'true',
    'data-offline-waiting-generation': '', ...overrides,
  }));
  const reads = [], attachments = [];
  const root = {
    getAttribute(name) {assert.ok(attributes.has(name), `unexpected attribute ${name}`); reads.push(name); return attributes.get(name);},
    querySelector(selector) {assert.equal(selector, '[data-offline-state]'); return {getAttribute(name) {assert.equal(name, 'data-offline-state'); reads.push(name); return 'error';}};},
    get scrollTop() {throw Error('scroll read prohibited');},
  };
  const document = {querySelector(selector) {assert.equal(selector, '[data-offline-controls]'); return root;}, querySelectorAll(selector) {assert.equal(selector, '[data-offline-controls]'); return [root];}};
  let evaluations = 0;
  const page = {isClosed: () => false, async evaluate(callback) {
    evaluations++;
    const previous = Object.getOwnPropertyDescriptor(globalThis, 'document');
    Object.defineProperty(globalThis, 'document', {configurable: true, value: document});
    try {return callback();} finally {if (previous) Object.defineProperty(globalThis, 'document', previous); else delete globalThis.document;}
  }};
  const context = {pages: () => [page]};
  const info = {async attach(name, {body, contentType}) {assert.ok(body.length <= 4096); assert.equal(contentType, 'application/json'); attachments.push({name, body: body.toString(), report: JSON.parse(body.toString())});}};
  return {prefix, reads, attachments, context, page, info, evaluations: () => evaluations};
}
function offlineScalarRetirementProof() {
  return {forcedParentEmptyOnly: true, lists: 4, lookups: 1, framesAdded: 1,
    framesRemoved: 1, framesConnected: 0, maximumLiveFrames: 1,
    nativeUpdateCalls: null, nativeUpdateWitness: 'disabled', nativeWitnessUnavailable: false,
    pendingFrameListeners: 0};
}

test('offline failure DOM observation preserves full fixed categories and exactly seven scalar reads', {concurrency: false}, async () => {
  for (const reason of ['controller-changed', 'waiting-worker-changed', 'registration-proof-empty-installing', 'parent-controller-changed', 'child-controller-changed', 'child-controller-invalid']) {
    const f = offlineDOMObservationFixture();
    const error = `${f.prefix} [${reason}]`;
    const g = offlineDOMObservationFixture({'data-offline-last-error': error});
    await observeOfflineUpdateFailure(g.context, g.info);
    const report = g.attachments[0].report;
    assert.equal(report.observation, 'observed'); assert.equal(report.values.lastError, error);
    assert.deepEqual(report.lastErrorCapture, {present: true, complete: true, truncated: false});
    assert.equal(g.evaluations(), 1); assert.equal(g.reads.length, 7);
    assert.deepEqual(Object.keys(report.values).sort(), ['completedOperations','controlling','enabled','lastError','operationError','renderedState','waitingGenerationPresent'].sort());
    assert.match(report.capturePhase, /late/); assert.match(report.meaning, /not operation-stage/);
  }
});
test('offline failure DOM observation suppresses unknown values without inventing truncation or absence', {concurrency: false}, async () => {
  const secret = 'private-unrecognized-'.repeat(300);
  const f = offlineDOMObservationFixture({'data-offline-last-error': secret});
  await observeOfflineUpdateFailure(f.context, f.info);
  assert.equal(f.attachments[0].report.values.lastError, 'unrecognized');
  assert.deepEqual(f.attachments[0].report.lastErrorCapture, {present: true, complete: false, truncated: null});
  assert.equal(f.attachments[0].body.includes('private-unrecognized-'), false);
  const absent = offlineDOMObservationFixture({'data-offline-last-error': null});
  await observeOfflineUpdateFailure(absent.context, absent.info);
  assert.deepEqual(absent.attachments[0].report.lastErrorCapture, {present: false, complete: false, truncated: null});
});
test('offline failure unavailable-page gates emit unavailable rather than false scalar evidence', {concurrency: false}, async () => {
  const f = offlineDOMObservationFixture();
  await observeOfflineUpdateFailure({pages: () => [f.page, f.page]}, f.info);
  assert.equal(f.evaluations(), 0); assert.equal(f.attachments[0].report.observation, 'unavailable');
  assert.equal(f.attachments[0].report.values, null);
  assert.deepEqual(f.attachments[0].report.lastErrorCapture, {present: null, complete: null, truncated: null});
});
test('offline fault retirement attaches fixed proof after cleanup and returns the original proof', async () => {
  const order = [], proof = offlineScalarRetirementProof(), page = {};
  proof.unselected = 'must-not-escape';
  let record;
  const result = await retireOfflineParentFault(page, {async attach(name, {body}) {order.push('attach'); assert.equal(name, 'forced-parent-empty-retirement-proof'); assert.ok(body.length <= 4096); assert.equal(body.toString().includes('must-not-escape'), false); record = JSON.parse(body.toString());}}, async actual => {assert.equal(actual, page); order.push('retire'); return proof;}, false);
  assert.equal(result, proof); assert.deepEqual(order, ['retire','attach']);
  assert.equal(record.retirementCall, 'returned'); assert.equal(record.proofComplete, true);
  assert.equal(record.proof.nativeUpdateCalls, null); assert.equal(record.proof.nativeUpdateWitness, 'disabled');
});
test('offline fault cleanup throw still fails a previously passing body and emits no raw error', async () => {
  const failure = Error('private-cleanup-error'); let record;
  await assert.rejects(retireOfflineParentFault({}, {async attach(_name, {body}) {assert.equal(body.toString().includes('private-cleanup-error'), false); record = JSON.parse(body.toString());}}, async () => {throw failure;}, false), error => error === failure);
  assert.equal(record.retirementCall, 'threw'); assert.equal(record.proof, null);
});
test('offline fault diagnostics cannot mask the primary assertion or prevent cleanup', async () => {
  const original = Error('original assertion'), cleanup = Error('cleanup'), order = [];
  const run = async () => {
    let primaryFailed = false;
    try {throw original;} catch (error) {primaryFailed = true; throw error;}
    finally {await retireOfflineParentFault({}, {async attach() {order.push('attach'); throw Error('attachment');}}, async () => {order.push('retire'); throw cleanup;}, primaryFailed);}
  };
  await assert.rejects(run(), error => error === original);
  assert.deepEqual(order, ['retire','attach']);
});
test('offline diagnostic attachment rejection preserves a successful retirement result', async () => {
  const proof = offlineScalarRetirementProof(); let calls = 0;
  const result = await retireOfflineParentFault({}, {async attach() {throw Error('attachment unavailable');}}, async () => {calls++; return proof;}, false);
  assert.equal(calls, 1); assert.equal(result, proof);
  const f = offlineDOMObservationFixture();
  await observeOfflineUpdateFailure(f.context, {async attach() {throw Error('attachment unavailable');}});
  assert.equal(f.evaluations(), 1);
});
test('offline retirement scalar projection preserves unknown rather than promoting it to zero', async () => {
  const proof = {...offlineScalarRetirementProof(), framesConnected: 'unknown', nativeUpdateWitness: 'unexpected'}; let report;
  await retireOfflineParentFault({}, {async attach(_name, {body}) {report = JSON.parse(body.toString());}}, async () => proof, false);
  assert.equal(report.proofComplete, false); assert.equal(report.proof.framesConnected, null);
  assert.equal(report.proof.nativeUpdateWitness, null);
});
