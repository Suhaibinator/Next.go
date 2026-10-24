import {spawn} from 'node:child_process';
import {join} from 'node:path';
const binary=process.env.NEXTGO_E2E_SERVER_BINARY||join(process.env.NEXTGO_E2E_ROOT,'production','server');
const child=spawn(binary,['-addr','127.0.0.1:4142'],{cwd:'/',stdio:'inherit',env:{...process.env,NEXTGO_DEMO_CSP:'true',NEXTGO_DEMO_EARLY_SCRIPTS:'true',NEXTGO_DEMO_BASE_PATH:'/safe',NEXTGO_DEMO_ASSET_PREFIX:'/security-assets'}});
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,()=>child.kill(signal));
child.on('exit',code=>process.exit(code??0));
