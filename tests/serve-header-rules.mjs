import {spawn} from 'node:child_process';
import {join} from 'node:path';
const binary=process.env.NEXTGO_E2E_SERVER_BINARY||join(process.env.NEXTGO_E2E_ROOT,'production','server');
const common={...process.env,NEXTGO_DEMO_HEADER_RULES:'true',NEXTGO_DEMO_RULES:'false',NEXTGO_DEMO_CSP:'false',NEXTGO_DEMO_EARLY_SCRIPTS:'false',NEXTGO_DEMO_TRACING:'false',NEXTGO_DEMO_OTLP_ENDPOINT:'',NEXTGO_DEMO_PROXY_ORIGIN:''};
const children=[
 spawn(binary,['-addr','127.0.0.1:4144'],{cwd:'/',stdio:'inherit',env:{...common,NEXTGO_DEMO_BASE_PATH:'/headers',NEXTGO_DEMO_ASSET_PREFIX:'/header-assets',NEXTGO_DEMO_LOCALES:'false',NEXTGO_DEMO_TRAILING_SLASH:'false'}}),
 spawn(binary,['-addr','127.0.0.1:4145'],{cwd:'/',stdio:'inherit',env:{...common,NEXTGO_DEMO_BASE_PATH:'/site',NEXTGO_DEMO_ASSET_PREFIX:'/header-assets',NEXTGO_DEMO_LOCALES:'true',NEXTGO_DEMO_LOCALE_DETECT:'false',NEXTGO_DEMO_LOCALE_PREFIX_DEFAULT:'true',NEXTGO_DEMO_LOCALE_DOMAINS:'',NEXTGO_DEMO_TRAILING_SLASH:'true'}}),
];
let stopped=false;const stop=()=>{if(stopped)return;stopped=true;for(const child of children)child.kill('SIGTERM')};
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,stop);
for(const child of children)child.on('exit',code=>{if(!stopped){stop();process.exit(code??1)}});
