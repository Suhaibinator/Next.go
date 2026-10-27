// Pure evidence/data adapter tests. They do not instantiate app or browser code.
import test from 'node:test';
import assert from 'node:assert/strict';
import {lambdaCases,adaptSharedSpec,nativeOnly,deadlineCase,checkHarness} from './retained-routes-lambda-ci.mjs';
import {lambdaApps} from './retained-routes-lambda-matrix.mjs';
const titles=Array.from({length:17},(_,i)=>'shared '+i).concat(deadlineCase);
function matrix(mode,executed){return{errors:[],config:{version:'1.64.1',workers:1,maxFailures:1,projects:['chromium','firefox','webkit','webkit-touch'].map(name=>({name:`retained-${mode}-${name}`,retries:0}))},stats:{expected:72,unexpected:0,flaky:0,skipped:0},suites:[{specs:titles.map(title=>({title,file:title===deadlineCase?'retained-routes-lambda.spec.mjs':'retained-shared.spec.mjs',tests:['chromium','firefox','webkit','webkit-touch'].map(profile=>({projectName:`retained-${mode}-${profile}`,expectedStatus:'passed',status:'expected',results:executed?[{status:'passed',retry:0,attachments:[]}]:[]}))}))}]};}
test('Lambda evidence requires exact68shared+4deadline counterparts on the selected transport',()=>{
  for(const mode of ['buffered','streaming']){assert.equal(lambdaCases(matrix(mode,false),mode,lambdaApps.retained,titles,false).length,72);assert.equal(lambdaCases(matrix(mode,true),mode,lambdaApps.retained,titles,true).length,72);}
  assert.throws(()=>lambdaCases(matrix('buffered',true),'streaming',lambdaApps.retained,titles,true));assert.throws(()=>lambdaCases(matrix('buffered',true),'buffered',lambdaApps.retained,titles.slice(0,-1).concat(nativeOnly),true));
});
test('Lambda evidence rejects missing duplicated skipped retried or mislabeled native cases',()=>{
  for(const mutate of [r=>r.suites[0].specs.pop(),r=>{r.suites[0].specs[1].title=r.suites[0].specs[0].title;},r=>{r.stats.skipped=1;},r=>{r.stats.flaky=1;},r=>{r.suites[0].specs[0].tests[0].results[0].retry=1;},r=>{r.suites[0].specs[0].file='retained-routes.spec.mjs';},r=>{r.suites[0].specs.at(-1).title=nativeOnly;},r=>{r.config.projects[0].retries=1;}]){const report=matrix('buffered',true);mutate(report);assert.throws(()=>lambdaCases(report,'buffered',lambdaApps.retained,titles,true));}
});
test('Shared-body adapter substitutes only the exacttwo import literals and preserves original test text',()=>{
  const source="import {startRetainedServer} from './retained-routes-fixture.mjs';\nimport {holdPage} from './link-status-gates.mjs';\nconst body='unchanged application and teardown assertions';\n";
  const actual=adaptSharedSpec(source,'file:///frozen/lambda.mjs','file:///frozen/gates.mjs');assert.equal(actual,source.replace("'./retained-routes-fixture.mjs'",'"file:///frozen/lambda.mjs"').replace("'./link-status-gates.mjs'",'"file:///frozen/gates.mjs"'));
  assert.throws(()=>adaptSharedSpec(source+"import './retained-routes-fixture.mjs';",'file:///frozen/lambda.mjs','file:///frozen/gates.mjs'));assert.throws(()=>adaptSharedSpec(source.replace('./link-status-gates.mjs','other'),'file:///frozen/lambda.mjs','file:///frozen/gates.mjs'));
});

import {mkdtemp,mkdir,writeFile,symlink,unlink,realpath,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
test('Materialized two-app adapters recheck every body and dependency after successful or failed work',async()=>{
  const root=await mkdtemp(join(tmpdir(),'nextgo-lambda-harness-audit-')),source=join(root,'source'),parent=join(root,'lambda-harness'),hash=value=>createHash('sha256').update(value).digest('hex');
  try{await mkdir(source);await mkdir(join(source,'node_modules'));await mkdir(join(source,'other-modules'));await mkdir(parent);const state={root,source,report:{harness:{}}};
    for(const app of Object.values(lambdaApps)){const folder=join(parent,app.id);await mkdir(folder);await writeFile(join(folder,app.sharedFile),app.id);await symlink(join(source,'node_modules'),join(folder,'node_modules'),'dir');state.report.harness[app.id]={folder,sharedSHA256:hash(app.id),nodeModules:await realpath(join(source,'node_modules'))};if(app.id==='retained'){await writeFile(join(folder,'retained-routes-lambda.spec.mjs'),'counterpart');state.report.harness[app.id].counterpartSHA256=hash('counterpart');}}
    await checkHarness(state);const folder=state.report.harness.merge.folder;await writeFile(join(folder,'merge-shared.spec.mjs'),'changed-after-matrix');await assert.rejects(checkHarness(state));assert.equal(state.report.harnessChecks.at(-1).sharedSHA256,hash('changed-after-matrix'));await writeFile(join(folder,'merge-shared.spec.mjs'),'merge');await unlink(join(folder,'node_modules'));await symlink(join(source,'other-modules'),join(folder,'node_modules'),'dir');await assert.rejects(checkHarness(state));
  }finally{await rm(root,{recursive:true,force:false});}
});
test('Merge adapter changes only its own exact fixture and gate imports',()=>{
  const source="import {test,expect} from './merge-fixture.mjs';\nimport {holdRequests} from './link-status-gates.mjs';\nconst original='all eighteen original merge bodies';\n";
  assert.equal(adaptSharedSpec(source,'file:///owned/merge-lambda-fixture.mjs','file:///owned/gates.mjs','merge'),source.replace("'./merge-fixture.mjs'",'"file:///owned/merge-lambda-fixture.mjs"').replace("'./link-status-gates.mjs'",'"file:///owned/gates.mjs"'));
  assert.throws(()=>adaptSharedSpec(source,'file:///owned/fixture','file:///owned/gates','retained'));
});
