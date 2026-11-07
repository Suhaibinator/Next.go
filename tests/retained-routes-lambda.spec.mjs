// The other17 bodies are imported unchanged from the native spec by the owner.
// This counterpart deliberately qualifies invocation-deadline cancellation.
import {test,expect,assertTaskDeadline} from './retained-routes-lambda-fixture.mjs';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const panel=page=>page.locator('[data-retained-panel]').filter({visible:true});
const part=(page,name)=>panel(page).locator(`[data-retained-${name}]`);
test('a serial Lambda task drains by its invocation deadline before retained navigation and never replays',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await part(page,'increment').click();await part(page,'draft').fill('Deadline-owned draft');
  await page.evaluate(()=>{globalThis.lambdaDocument=document;globalThis.lambdaA=document.querySelector('[data-retained-panel]');globalThis.lambdaCount=lambdaA.querySelector('[data-retained-count]');globalThis.lambdaText=lambdaCount.firstChild;});
  native.expectTaskDeadline();await part(page,'task').click();
  const started=await native.waitFor(state=>state.observations.some(item=>item.kind==='dispatch'&&item.path==='/api/task'));
  const dispatch=started.observations.find(item=>item.kind==='dispatch'&&item.path==='/api/task');
  // No health poll is admitted behind the serial long invocation.
  await page.getByRole('navigation',{name:'Retained example'}).getByRole('link',{name:'B',exact:true}).click();
  const waiting=await native.waitFor(state=>state.observations.some(item=>item.kind==='queued'&&item.path==='/b'));
  expect(waiting.reports.some(item=>item.path==='/api/task')).toBe(false);expect(waiting.active).toBe(dispatch.id);
  const settled=await native.waitFor(state=>state.reports.some(item=>item.kind==='error'&&item.path==='/api/task')&&state.observations.some(item=>item.kind==='dispatch'&&item.path==='/b'),{timeout:20_000});
  assertTaskDeadline(settled,dispatch);
  const report=settled.reports.find(item=>item.kind==='error'&&item.path==='/api/task'),next=settled.observations.find(item=>item.kind==='dispatch'&&item.path==='/b');
  expect(report.id).toBe(dispatch.id);expect(report.ack).toBe(202);expect(JSON.stringify(report)).toContain('context deadline exceeded');expect(next.order).toBeGreaterThan(report.order);
  await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','B');
  const health=await(await page.request.get('/api/health')).json();expect(health).toMatchObject({pid:native.snapshot().pid,tasksStarted:1,tasksCanceled:1,tasksActive:0});
  const ticks=await page.evaluate(()=>Number(lambdaA.querySelector('[data-retained-ticks]').textContent));await expect.poll(async()=>Number(await part(page,'ticks').textContent())).toBeGreaterThan(3);
  expect(await page.evaluate(()=>Number(lambdaA.querySelector('[data-retained-ticks]').textContent))).toBe(ticks);
  await page.goBack();await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','A');
  expect(await page.evaluate(()=>document===lambdaDocument&&document.querySelector('[data-nextgo-view-active] [data-retained-panel]')===lambdaA&&lambdaA.querySelector('[data-retained-count]')===lambdaCount&&lambdaCount.firstChild===lambdaText)).toBe(true);
  await expect(part(page,'count')).toHaveText('3');await expect(part(page,'draft')).toHaveValue('Deadline-owned draft');await expect(part(page,'setups')).toHaveText('2');await expect(part(page,'cleanups')).toHaveText('1');await expect(part(page,'task-canceled')).toHaveText('1');await expect(part(page,'task-completed')).toHaveText('0');
  const final=await(await page.request.get('/api/health')).json();expect(final).toMatchObject({pid:health.pid,tasksStarted:1,tasksCanceled:1,tasksActive:0});
  expect(native.snapshot().lambda.observations.filter(item=>item.kind==='dispatch'&&item.path==='/api/task')).toHaveLength(1);
});
