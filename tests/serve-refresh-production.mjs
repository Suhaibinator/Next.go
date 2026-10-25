import {startRefreshProduction} from './refresh-production-fixture.mjs';

const worker = await startRefreshProduction(process.env.NEXTGO_REFRESH_TRANSPORT || 'http');
console.log(`Refresh production fixture ${JSON.stringify(worker.proof)}`);
let closing;
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  closing ||= worker.close().catch(error => {console.error(error); process.exitCode = 1;});
});
