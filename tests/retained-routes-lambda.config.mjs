import {defineConfig} from '@playwright/test';
import {isAbsolute} from 'node:path';
import {lambdaApps,lambdaProfiles,lambdaModes,nativeOnly} from './retained-routes-lambda-matrix.mjs';
const app=process.env.NEXTGO_RETAINED_LAMBDA_APP,mode=process.env.NEXTGO_RETAINED_LAMBDA_MODE,folder=process.env.NEXTGO_RETAINED_LAMBDA_TEST_DIR;
if(!Object.hasOwn(lambdaApps,app)||!lambdaModes.includes(mode)||!isAbsolute(folder||''))throw Error('Explicit reviewed app, LOCAL mode and owned materialized directory required');
const spec=lambdaApps[app];
export default defineConfig({testDir:folder,testMatch:app==='retained'?[spec.sharedFile,'retained-routes-lambda.spec.mjs']:[spec.sharedFile],...(app==='retained'?{grepInvert:new RegExp(nativeOnly+'$')}:{}),outputDir:process.env.NEXTGO_E2E_RESULTS,workers:1,fullyParallel:false,retries:0,maxFailures:1,timeout:90_000,expect:{timeout:15_000},globalTimeout:1_200_000,use:{baseURL:'http://127.0.0.1:4188',headless:true,trace:'retain-on-failure'},projects:lambdaProfiles.map(name=>({name:`${app}-${mode}-${name}`,use:{browserName:name==='webkit-touch'?'webkit':name,...(name==='webkit-touch'?{viewport:{width:390,height:844},isMobile:true,hasTouch:true}:{}),...(name==='chromium'&&process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH?{launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH}}:{})}}))});
