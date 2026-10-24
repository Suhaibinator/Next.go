import {spawn} from 'node:child_process';
import {cp,mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';

const framework=fileURLToPath(new URL('..',import.meta.url));
const root=join(process.env.NEXTGO_E2E_ROOT,'workspace');
const output=join(process.env.NEXTGO_E2E_ROOT,'workspace-production');
await mkdir(root,{recursive:true});await mkdir(output,{recursive:true});
await cp('examples/workspace',root,{recursive:true,filter:path=>!path.split('/').includes('.nextgo')});
await writeFile(join(root,'go.work'),`go 1.24.0\nuse (\n ${JSON.stringify(framework)}\n ./site\n ./shared\n)\n`);
const cli=join(process.env.NEXTGO_E2E_ROOT,'workspace-nextgo');
async function run(command,args){const child=spawn(command,args,{stdio:'inherit'});const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',resolve)});if(code!==0)process.exit(1)}
await run('go',['build','-o',cli,'./cmd/nextgo']);
await run(cli,['build','-out',output,join(root,'site')]);
const environment={...process.env,AWS_LAMBDA_RUNTIME_API:''};
const children=[spawn(join(output,'server'),['-addr','127.0.0.1:4139'],{cwd:output,stdio:'inherit',env:environment}),spawn(cli,['dev','-addr','127.0.0.1:4138',join(root,'site')],{stdio:'inherit',env:environment})];
let stopping=false;
function stop(){if(stopping)return;stopping=true;for(const child of children)child.kill('SIGTERM')}
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,stop);
for(const child of children){child.on('error',error=>{console.error(error);stop();process.exitCode=1});child.on('exit',code=>{stop();process.exitCode=code??0})}
