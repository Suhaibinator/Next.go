import {expect} from '@playwright/test';

// Body waits use the existing bounded assertion timeout. Every native gate is
// released by close(), which also joins every handler, including non-page ones.
export async function holdRequests(page, pattern, matches, failure = false) {
  let release, closing, entries = 0;
  const gate = new Promise(resolve => { release = resolve; });
  const active = new Set(), jobs = [];
  const run = async route => {
    if (!matches(route.request())) return route.continue();
    entries++;
    await gate;
    try { if (failure) await route.abort('failed'); else await route.continue(); }
    catch (error) {
      if (!/Route is already handled|Target page, context or browser has been closed|net::ERR_ABORTED|NS_BINDING_ABORTED|Request was cancelled/i.test(String(error))) throw error;
    }
  };
  const handler = route => {
    if (jobs.length >= 32) throw new Error('link gate request bound exceeded');
    const job = run(route);
    jobs.push(job); active.add(job);
    job.finally(() => active.delete(job)).catch(() => {});
    return job;
  };
  await page.route(pattern, handler);
  return {
    release,
    waitForEntry: () => expect.poll(() => entries, {message: `actual route entered ${pattern}`}).toBeGreaterThan(0),
    close() {
      if (closing) return closing;
      closing = (async () => {
        release();
        await page.unroute(pattern, handler);
        await expect.poll(() => active.size, {message: `all ${pattern} route handlers drained`}).toBe(0);
        const outcomes = await Promise.allSettled(jobs);
        const failed = outcomes.filter(outcome => outcome.status === 'rejected');
        if (failed.length) throw new AggregateError(failed.map(outcome => outcome.reason), 'link gate route handlers failed');
      })();
      return closing;
    },
  };
}
export function holdPage(page, path, failure = false) {
  return holdRequests(page, `**${path}`, request => request.headers()['x-nextgo-navigate'] === '1' && request.headers()['x-nextgo-purpose'] !== 'prefetch', failure);
}

// Preserve genuine server headers/bytes while holding after the first shell.
// Limits mirror internal/navigation/stream.go; this is test scheduling only.
export async function installStreamHold(page, pathname) {
  await page.evaluate(path => {
    const original = window.fetch;
    const maxFrame = 2 << 20, maxBytes = 8 << 20, maxFrames = 130;
    const state = {started: false, done: false, eof: false, canceled: false, releasedLock: false, cancelDone: true, bytes: 0, frames: 0, error: '', shell: null};
    let reader, release, pump, cancelJob, closed = false;
    const gate = new Promise(resolve => { release = resolve; });
    const cancel = () => {
      release();
      if (!reader || state.done || cancelJob) return;
      state.canceled = true; state.cancelDone = false;
      cancelJob = reader.cancel('link-status fixture cleanup').catch(error => { state.error ||= String(error); }).finally(() => { state.cancelDone = true; });
    };
    window.linkStreamHold = {state, release, cancel};
    window.fetch = async (...args) => {
      const response = await original(...args);
      if (closed || new URL(response.url).pathname !== path || !response.headers.get('content-type')?.startsWith('application/x-nextgo-stream+json')) return response;
      if (state.started) throw new Error('link stream fixture accepts one response');
      state.started = true;
      reader = response.body.getReader();
      let prefix = new Uint8Array(0), shell = false, frameBytes = 0;
      const account = bytes => {
        if (bytes.length > maxBytes - state.bytes) throw new Error('link stream total byte bound exceeded');
        state.bytes += bytes.length;
        for (const value of bytes) {
          if (value === 10) { state.frames++; frameBytes = 0; if (state.frames > maxFrames) throw new Error('link stream frame count exceeded'); }
          else if (++frameBytes > maxFrame) throw new Error('link stream frame byte bound exceeded');
        }
      };
      const body = new ReadableStream({
        start(controller) {
          pump = (async () => {
            try {
              while (!state.canceled) {
                const next = await reader.read();
                if (next.done) {
                  state.eof = !state.canceled;
                  if (!shell || frameBytes !== 0) throw new Error('link stream EOF before complete frames');
                  if (!state.canceled) controller.close();
                  return;
                }
                account(next.value);
                if (shell) { controller.enqueue(next.value); continue; }
                const end = next.value.indexOf(10);
                const take = end < 0 ? next.value.length : end + 1;
                if (prefix.length + take > maxFrame + 1) throw new Error('link stream shell prefix bound exceeded');
                const first = new Uint8Array(prefix.length + take);
                first.set(prefix); first.set(next.value.subarray(0, take), prefix.length);
                if (end < 0) { prefix = first; continue; }
                const frame = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(first.subarray(0, first.length - 1)));
                if (frame.type !== 'shell' || frame.version !== 1 || !frame.page || frame.page.version !== 3 || frame.page.status !== 200 || new URL(frame.page.url, response.url).pathname !== path || !frame.page.html.includes('data-link-content-pending')) throw new Error('first genuine frame is not the expected page shell');
                state.shell = {type: frame.type, version: frame.version, pageVersion: frame.page.version, status: frame.page.status, url: frame.page.url, title: frame.page.title, build: frame.page.build, configuration: frame.page.configuration, protocolHeader: response.headers.get('x-nextgo-protocol'), configurationHeader: response.headers.get('x-nextgo-configuration')};
                controller.enqueue(first); prefix = new Uint8Array(0); shell = true;
                await gate;
                if (!state.canceled && take < next.value.length) controller.enqueue(next.value.subarray(take));
              }
            } catch (error) {
              if (!state.canceled) { state.error = String(error); controller.error(error); }
            } finally {
              if (!state.eof && !state.canceled) cancel();
              try { reader.releaseLock(); state.releasedLock = true; }
              catch (error) { state.error ||= String(error); }
              state.done = true;
            }
          })();
        },
        cancel() { cancel(); return cancelJob; },
      });
      const held = new Response(body, {status: response.status, statusText: response.statusText, headers: response.headers});
      Object.defineProperty(held, 'url', {value: response.url});
      return held;
    };
    window.linkStreamHold.restore = () => { closed = true; window.fetch = original; cancel(); };
    window.linkStreamHold.join = () => Promise.allSettled([pump, cancelJob].filter(Boolean));
  }, pathname);
  return {
    release: () => page.evaluate(() => window.linkStreamHold.release()),
    async close() {
      await page.evaluate(() => window.linkStreamHold.restore());
      await expect.poll(() => page.evaluate(() => {
        const state = window.linkStreamHold.state;
        return !state.started || state.done && state.cancelDone && state.releasedLock;
      }), {message: 'stream pump, cancellation and reader lock drained'}).toBe(true);
      // All stored jobs are already settled after that assertion.
      await page.evaluate(() => window.linkStreamHold.join());
      expect(await page.evaluate(() => window.linkStreamHold.state.error)).toBe('');
    },
  };
}
