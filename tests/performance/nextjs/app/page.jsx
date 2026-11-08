import Link from 'next/link';
import Counter from './counter';

export const dynamic = 'force-dynamic';

export default function Page() {
  return <main data-comparison-page="home">
    <h1>Framework comparison</h1>
    <p>Rendered on the server.</p>
    <Counter initial={3}/>
    <Link href="/about" prefetch={false} data-about="">About</Link>
  </main>;
}
