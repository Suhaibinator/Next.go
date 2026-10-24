import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';

const output=join(process.env.NEXTGO_E2E_ROOT,'workspace-production');await mkdir(output,{recursive:true});
let binary=process.env.NEXTGO_E2E_WORKSPACE_BINARY;
if(!binary){const build=spawn('go',['run','./cmd/nextgo','build','-out',output,'./examples/workspace/site'],{stdio:'inherit'});const code=await new Promise((resolve,reject)=>{build.once('error',reject);build.once('exit',resolve)});if(code!==0)process.exit(1);binary=join(output,'server')}
const server=spawn(binary,['-addr','127.0.0.1:4139'],{cwd:output,stdio:'inherit',env:{...process.env,AWS_LAMBDA_RUNTIME_API:''}});
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,()=>server.kill(signal));server.on('exit',code=>process.exit(code??0));
