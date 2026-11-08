import Link from 'next/link';

export const dynamic = 'force-dynamic';

export default function Page() {
  return <main data-comparison-page="about">
    <h1>About comparison</h1>
    <p>This route is rendered for each request.</p>
    <Link href="/" prefetch={false}>Home</Link>
  </main>;
}
