// A passive record of the ORIGINAL upstream bytes, independent of DevTools'
// response-body cache. Snapshots contain framing metadata only, never HTML or
// typed result data. Observing an invalid stream does not alter its transport.
export const wireLimits = Object.freeze({ frame: 2 << 20, bytes: 8 << 20, frames: 131 });

export function createActionViewWireObserver() {
  const types = [], versions = [], decoder = new TextDecoder('utf-8', { fatal: true });
  let pending = Buffer.alloc(0), bytes = 0, error = false, complete = false, ended = false, phase = 'result';
  const fail = () => { error = true; complete = false; pending = Buffer.alloc(0); };
  function frame(data) {
    const value = JSON.parse(decoder.decode(data));
    if (!value || typeof value !== 'object' || Array.isArray(value) || value.version !== 1 || typeof value.type !== 'string' || types.length >= wireLimits.frames) return fail();
    if (phase === 'result') { if (value.type !== 'result') return fail(); phase = 'shell'; }
    else if (phase === 'shell') { if (value.type === 'shell') phase = 'chunks'; else if (value.type === 'view-error') phase = 'terminal'; else return fail(); }
    else if (phase === 'chunks') { if (value.type === 'done' || value.type === 'view-error') phase = 'terminal'; else if (value.type !== 'chunk') return fail(); }
    else return fail();
    types.push(value.type); versions.push(value.version);
  }
  return {
    feed(chunk) {
      if (error) return;
      if (ended || !(chunk instanceof Uint8Array)) return fail();
      if (chunk.byteLength > wireLimits.bytes - bytes) { bytes += chunk.byteLength; return fail(); }
      bytes += chunk.byteLength;
      const data = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
      let offset = 0;
      try {
        while (offset < data.length) {
          const newline = data.indexOf(10, offset), end = newline < 0 ? data.length : newline;
          if (end - offset > wireLimits.frame - pending.length) return fail();
          const part = data.subarray(offset, end);
          if (newline < 0) {
            // Own only the unfinished record, rather than retaining a view into
            // a potentially larger HTTP chunk's backing buffer.
            pending = pending.length ? Buffer.concat([pending, part]) : Buffer.from(part);
            return;
          }
          const record = pending.length ? Buffer.concat([pending, part]) : part;
          pending = Buffer.alloc(0); frame(record);
          if (error) return;
          offset = newline + 1;
        }
      } catch { fail(); }
    },
    end() {
      if (ended) return;
      ended = true;
      if (error || pending.length || phase !== 'terminal') fail(); else complete = true;
      pending = Buffer.alloc(0);
    },
    fail,
    snapshot() { return { frameTypes: [...types], versions: [...versions], bytes, complete, error }; },
  };
}
