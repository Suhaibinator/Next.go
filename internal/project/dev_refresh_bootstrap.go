package project

import "github.com/Suhaibinator/Next.go/internal/devrefresh"

// Included only for opted-in development modules and the exact compiler-paired
// wasm_exec.js digest admitted by devrefresh. The Go host owns refresh policy;
// this adapter only retires private shim timers at the runtime's real exit.
const devRefreshBootstrapBridge = `;(() => {
  const started = new WeakSet();
  Object.defineProperty(globalThis, '__nextgoDevRuntime', {value: Object.freeze({
    version: 1,
    shimDigest: '` + devrefresh.ShimDigest + `',
    start(go, instance) {
      if (!go || typeof go.run !== 'function' || typeof go.exit !== 'function' ||
          !(go._scheduledTimeouts instanceof Map) || started.has(go)) {
        throw new Error('Unsupported development Go runtime');
      }
      started.add(go);
      const exit = go.exit;
      go.exit = code => {
        go.__nextgoDevExitCode = code;
        for (const handle of go._scheduledTimeouts.values()) clearTimeout(handle);
        go._scheduledTimeouts.clear();
        return Reflect.apply(exit, go, [code]);
      };
      return go.run(instance);
    }
  })});
})();
`
