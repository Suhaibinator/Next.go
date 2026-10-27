// Additive bounded evidence only. No binary, ZIP, dependency tree or arbitrary
// directory scan is uploaded. The native collector retains its original paths.
import {realpath} from 'node:fs/promises';
import {join,basename,isAbsolute,resolve,relative,sep} from 'node:path';
import {check,json} from './static-shell-ci-build.mjs';
import {lambdaAppOrder,lambdaModes} from './retained-routes-lambda-matrix.mjs';
const outside=(parent,path)=>{const value=relative(parent,path);return isAbsolute(value)||value==='..'||value.startsWith('..'+sep);};
export async function collectLambdaEvidence(root,copy){
  await copy(join(root,'lambda-campaign.json'),'lambda-campaign.json',16<<20);
  for(const id of lambdaAppOrder){await copy(join(root,'lambda-'+id+'-http-baseline.json'),'lambda-'+id+'-http-baseline.json',4<<20);await copy(join(root,'dist','lambda-'+id+'-package','deployment.json'),'lambda-'+id+'-deployment.json',1<<20);}
  for(const mode of lambdaModes){await copy(join(root,'lambda-'+mode+'-browser-proof.json'),'lambda-'+mode+'-browser-proof.json',8<<20);for(const id of lambdaAppOrder){const prefix='lambda-'+mode+'-'+id;await copy(join(root,prefix+'-assets.json'),prefix+'-assets.json',4<<20);for(const name of ['discovery','results'])await copy(join(root,'report',prefix+'-'+name+'.json'),prefix+'-'+name+'.json.gz',name==='results'?128<<20:8<<20,true);}}
}
export async function collectLambdaTrace(root,copy){
  try{const failures=[];let observed=false;
    for(const mode of lambdaModes)for(const id of lambdaAppOrder){let report;try{report=await json(join(root,'report','lambda-'+mode+'-'+id+'-results.json'),128<<20);}catch{continue;}if(!(report.stats?.unexpected||report.stats?.flaky||report.errors?.length))continue;observed=true;const pending=(report.suites||[]).map(suite=>[suite,0]);let visits=0;
      while(pending.length){const [suite,depth]=pending.pop();check(++visits<=4096&&depth<=16,'bounded Lambda failure suites');for(const spec of suite.specs||[])for(const test of spec.tests||[])for(const value of test.results||[]){check(++visits<=4096,'bounded Lambda failure attempts');if(['failed','timedOut'].includes(value.status)){const time=Date.parse(value.startTime);check(Number.isSafeInteger(time),'actual failure time');failures.push({mode,app:id,value,time});}}for(const child of suite.suites||[])pending.push([child,depth+1]);}
    }
    if(!observed)return {status:'not-required'};failures.sort((a,b)=>a.time-b.time);const first=failures[0],attachments=first?.value.attachments?.filter(item=>item.name==='trace'&&item.contentType==='application/zip')||[];check(attachments.length===1,'one actual first Lambda trace required');const path=attachments[0].path;
    check(typeof path==='string'&&path.length<=4096&&isAbsolute(path)&&resolve(path)===path&&!outside(join(root,'browser','lambda-'+first.mode,first.app),path)&&basename(path)==='trace.zip'&&await realpath(path)===path,'same actual app/mode first trace required');await copy(path,'lambda-first-failure-trace.zip',8<<20);return {status:'attempted',mode:first.mode,app:first.app,path};
  }catch(error){return {status:'unavailable',reason:String(error.code||error.message).slice(0,160)};}
}
