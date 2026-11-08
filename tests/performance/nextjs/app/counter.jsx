'use client';

import {useEffect, useState} from 'react';

export default function Counter({initial}) {
  const [count, setCount] = useState(initial);
  const [ready, setReady] = useState(false);
  useEffect(() => { setReady(true); }, []);
  useEffect(() => { if (ready) performance.mark('comparison:ready'); }, [ready]);
  return <section aria-label="Counter" data-comparison-counter="" data-ready={ready}>
    <output data-count="" aria-live="polite">{count}</output>
    <button type="button" data-increment="" disabled={!ready} onClick={() => setCount(count + 1)}>Increment</button>
  </section>;
}
