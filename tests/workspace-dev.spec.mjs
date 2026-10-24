import {test,expect} from '@playwright/test';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const root=()=>join(process.env.NEXTGO_E2E_ROOT,'workspace');
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');

test('sibling Go edits rebuild both targets, preserve the last successful build on errors and reload once',async({page,request})=>{
 test.setTimeout(120_000);const file=join(root(),'shared/value.go');const original=await readFile(file,'utf8');
 await page.goto('/');await ready(page);let reloads=0;page.on('domcontentloaded',()=>reloads++);
 try{
  await writeFile(file,original.replace('Workspace counter','Edited sibling counter').replace('Step = 2','Step = 3'));
  await expect(page.locator('[data-workspace-label]')).toHaveText('Edited sibling counter',{timeout:45_000});await ready(page);await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('3');
  expect(await(await request.get('/api/health')).json()).toMatchObject({label:'Edited sibling counter'});await page.evaluate(()=>window.workspaceLastGood=true);
  await writeFile(file,original+'\nthis is invalid Go\n');await expect(page.locator('#nextgo-status')).toContainText('Build failed',{timeout:45_000});expect(await page.evaluate(()=>window.workspaceLastGood)).toBe(true);await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('6');
  await writeFile(file,original);await expect(page.locator('[data-workspace-label]')).toHaveText('Workspace counter',{timeout:45_000});await ready(page);await page.waitForTimeout(1200);expect(reloads).toBe(2);
 }finally{await writeFile(file,original)}
});

test('embedded sibling data changes rebuild the published server',async({page})=>{
 test.setTimeout(120_000);const file=join(root(),'shared/note.txt');const original=await readFile(file,'utf8');
 await page.goto('/');await ready(page);
 try{await writeFile(file,'Edited embedded repository data.\n');await expect(page.locator('[data-workspace-note]')).toHaveText('Edited embedded repository data.\n',{timeout:45_000});await ready(page);await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('2')}
 finally{await writeFile(file,original);await expect(page.locator('[data-workspace-note]')).toContainText('This text was embedded from a sibling Go module.',{timeout:45_000});await ready(page)}
});

test('broken module and workspace manifests recover after external fixes without application edits',async({page})=>{
 test.setTimeout(150_000);const work=join(root(),'go.work'),mod=join(root(),'shared/go.mod');const originalWork=await readFile(work,'utf8'),originalMod=await readFile(mod,'utf8');
 await page.goto('/');await ready(page);
 try{
  for(const [file,original]of [[mod,originalMod],[work,originalWork]]){
   await page.evaluate(()=>window.workspaceLastGood=true);await writeFile(file,original+'\ninvalid manifest directive\n');await expect(page.locator('#nextgo-status')).toContainText('Build failed',{timeout:45_000});expect(await page.evaluate(()=>window.workspaceLastGood)).toBe(true);
   await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('2');
   await writeFile(file,original);await expect.poll(()=>page.evaluate(()=>window.workspaceLastGood),{timeout:45_000}).toBe(undefined);await ready(page);await expect(page.locator('[data-workspace-count]')).toHaveText('0');
  }
 }finally{await writeFile(mod,originalMod);await writeFile(work,originalWork)}
});
