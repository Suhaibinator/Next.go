import assert from 'node:assert/strict';
import test from 'node:test';
import { createActionViewWireObserver, wireLimits } from './action-view-wire-observer.mjs';

const encode = value => Buffer.from(JSON.stringify({ version: 1, ...value }) + '\n');
const result = encode({ type: 'result', result: { data: { count: 1 } } });
const shell = encode({ type: 'shell', page: { html: 'Useful SSR café 🌿' } });
const done = encode({ type: 'done' });
const collect = parts => { const observer = createActionViewWireObserver(); for (const part of parts) observer.feed(part); observer.end(); return observer.snapshot(); };

test('arbitrary byte boundaries preserve UTF-8 framing and snapshots own metadata', () => {
  const wire = Buffer.concat([result, shell, encode({ type: 'chunk', id: 'detail', html: 'Private café 🌿 text' }), done]);
  for (const size of [1, 2, 3, 7, 31, wire.length]) {
    const observer = createActionViewWireObserver();
    for (let offset = 0; offset < wire.length; offset += size) observer.feed(wire.subarray(offset, offset + size));
    observer.end(); const snapshot = observer.snapshot();
    assert.deepEqual(snapshot, { frameTypes: ['result', 'shell', 'chunk', 'done'], versions: [1, 1, 1, 1], bytes: wire.length, complete: true, error: false });
    assert.equal(JSON.stringify(snapshot).includes('Private'), false);
    snapshot.frameTypes.push('forged'); snapshot.versions[0] = 99;
    assert.equal(observer.snapshot().frameTypes.length, 4); assert.equal(observer.snapshot().versions[0], 1);
  }
});

test('unfinished bytes are owned rather than retaining a caller-mutated chunk', () => {
  const observer = createActionViewWireObserver(), partial = Buffer.from(result.subarray(0, 8));
  observer.feed(partial); partial.fill(120);
  observer.feed(result.subarray(8)); observer.feed(shell); observer.feed(done); observer.end();
  assert.equal(observer.snapshot().complete, true);
});

test('truncation, invalid UTF-8, terminal trailing bytes and invalid ordering cannot report success', () => {
  for (const parts of [
    [result, shell, done.subarray(0, -1)], [result, shell], [result, done],
    [result, shell, done, Buffer.from('x')], [result, shell, done, Buffer.from('\n')],
    [result, Buffer.concat([Buffer.from('{"version":1,"type":"shell","page":{"html":"'), Buffer.from([0xc3, 0x28]), Buffer.from('"}}\n')])],
  ]) { const value = collect(parts); assert.equal(value.error, true); assert.equal(value.complete, false); }
  const interrupted = createActionViewWireObserver(); interrupted.feed(result); interrupted.fail(); interrupted.feed(shell); interrupted.feed(done); interrupted.end();
  assert.equal(interrupted.snapshot().error, true); assert.equal(interrupted.snapshot().complete, false);
});

test('frame, total-byte and frame-count admission are bounded at their edges', () => {
  const overhead = encode({ type: 'shell', page: { html: '' } }).length - 1;
  const exact = encode({ type: 'shell', page: { html: 'x'.repeat(wireLimits.frame - overhead) } });
  assert.equal(exact.length - 1, wireLimits.frame);
  assert.equal(collect([result, exact, done]).complete, true);
  const tooLarge = encode({ type: 'shell', page: { html: 'x'.repeat(wireLimits.frame - overhead + 1) } });
  assert.equal(collect([result, tooLarge, done]).error, true);
  const bytes = createActionViewWireObserver(); bytes.feed(Buffer.alloc(wireLimits.bytes + 1, 120)); bytes.end();
  assert.equal(bytes.snapshot().error, true); assert.equal(bytes.snapshot().complete, false);
  const boundary = Array.from({ length: 128 }, (_, index) => encode({ type: 'chunk', id: `chunk${index}`, html: '' }));
  assert.equal(collect([result, shell, ...boundary, done]).frameTypes.length, 131);
  assert.equal(collect([result, shell, ...boundary, done]).complete, true);
  const excess = collect([result, shell, ...boundary, encode({ type: 'chunk', id: 'extra', html: '' }), done]);
  assert.equal(excess.error, true); assert.equal(excess.complete, false); assert.ok(excess.frameTypes.length <= wireLimits.frames);
});
