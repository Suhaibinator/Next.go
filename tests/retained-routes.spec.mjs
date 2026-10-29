import {test as base,expect} from '@playwright/test';
import {startRetainedServer} from './retained-routes-fixture.mjs';
import {holdRequests,holdPage} from './link-status-gates.mjs';
import {armRetainedScrollProbe,collectRetainedScrollProbe} from './retained-scroll-diagnostics.mjs';

const test=base.extend({native:async({page},use,info)=>{
  const startup=new AbortController();const closed=()=>startup.abort(Error('Owned page closed during startup'));page.once('close',closed);
  let native;
  try{native=await startRetainedServer({signal:startup.signal});}
  catch(error){if(error.retainedProof)await info.attach('retained-startup-proof',{body:JSON.stringify(error.retainedProof),contentType:'application/json'});throw error;}
  finally{page.off('close',closed);}
  try{await use(native);}finally{
    const errors=[];try{await page.close();}catch(error){errors.push(error);}try{await native.close();}catch(error){errors.push(error);}
    const proof=native.snapshot();await info.attach('retained-native-process-proof',{body:JSON.stringify(proof),contentType:'application/json'});
    expect(proof.closed&&proof.joined&&proof.shutdownRequested&&proof.termSent&&proof.cwdRemoved&&proof.listenerRefused).toBe(true);
    expect(proof.forced||proof.unexpectedExit).toBe(false);expect(proof.code).toBe(0);expect(proof.signal).toBeNull();
    if(process.platform!=='win32')expect(proof.groupEmpty).toBe(true);
    if(errors.length)throw new AggregateError(errors,'Retained fixture cleanup failed');
  }
}});
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const panel=page=>page.locator('[data-retained-panel]').filter({visible:true});
const part=(page,name)=>panel(page).locator(`[data-retained-${name}]`);
async function go(page,name){await page.getByRole('navigation',{name:'Retained example'}).getByRole('link',{name,exact:true}).click();await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel',name);}
async function status(page){return (await page.request.get('/api/health')).json();}
async function control(page,value){const response=await page.request.post('/api/control',{data:value});expect(response.status()).toBe(200);}
async function saveA(page){await page.evaluate(()=>{globalThis.retainedDocument=document;const root=[...document.querySelectorAll('[data-retained-panel]')].find(node=>node.getAttribute('data-retained-panel')==='A');globalThis.retainedA={owner:root.closest("main"),root,count:root.querySelector('[data-retained-count]'),text:root.querySelector('[data-retained-count]').firstChild,draft:root.querySelector('[data-retained-draft]'),details:root.querySelector('[data-retained-details]')};});}
async function backA(page){await page.goBack();await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','A');}

test('meaningful SSR and hydration precede exact retained counter draft and details identity',async({page,native})=>{
  const core=await holdRequests(page,'**/app.wasm',()=>true);
  try{
    await page.goto(native.origin+'/a',{waitUntil:'domcontentloaded'});await core.waitForEntry();
    await expect(page.getByRole('heading',{level:1})).toHaveText('Page A · version 1');await expect(part(page,'count')).toHaveText('2');await saveA(page);
    await expect(page.locator('#nextgo-app > main')).toHaveCount(1);
    await expect.poll(()=>page.locator('#nextgo-app > main').evaluate(node=>getComputedStyle(node).getPropertyValue('--retained-direct-child-proof').trim())).toBe('yes');
    core.release();await ready(page);await expect(part(page,'setups')).toHaveText('1');
    expect(await page.evaluate(()=>retainedA.count===document.querySelector('[data-retained-count]')&&retainedA.text===retainedA.count.firstChild)).toBe(true);
    await part(page,'increment').click();await part(page,'draft').fill('Keep this draft');await part(page,'details').locator('summary').click();
    await go(page,'B');const before=(await status(page)).pageLoads;
    expect(await page.evaluate(()=>retainedA.root.isConnected&&retainedDocument===document)).toBe(true);
    await page.evaluate(()=>retainedA.root.querySelector('[data-retained-increment]').click());
    await backA(page);expect((await status(page)).pageLoads).toBeGreaterThan(before);
    expect(await page.evaluate(()=>retainedA.root===document.querySelector('[data-nextgo-view-active] [data-retained-panel]')&&retainedA.count.firstChild===retainedA.text&&retainedA.draft===retainedA.root.querySelector('[data-retained-draft]')&&retainedA.details.open&&retainedA.owner.parentElement===document.getElementById("nextgo-app")&&getComputedStyle(retainedA.owner).getPropertyValue("--retained-direct-child-proof").trim()==="yes")).toBe(true);
    await expect(part(page,'count')).toHaveText('3');await expect(part(page,'draft')).toHaveValue('Keep this draft');
    await expect(part(page,'setups')).toHaveText('2');await expect(part(page,'cleanups')).toHaveText('1');
  }finally{await core.close();}
});

test('hidden effects stop and a real owned HTTP read is canceled without replay',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);
  await expect.poll(async()=>Number(await part(page,'ticks').textContent())).toBeGreaterThan(0);
  await part(page,'task').click();await expect.poll(async()=>(await status(page)).tasksStarted).toBe(1);
  await go(page,'B');await expect.poll(async()=>(await status(page)).tasksCanceled).toBe(1);
  const ticks=await page.evaluate(()=>Number(retainedA.root.querySelector('[data-retained-ticks]').textContent));
  await expect.poll(async()=>Number(await part(page,'ticks').textContent())).toBeGreaterThan(3);
  expect(await page.evaluate(()=>Number(retainedA.root.querySelector('[data-retained-ticks]').textContent))).toBe(ticks);
  await backA(page);await expect(part(page,'setups')).toHaveText('2');await expect(part(page,'task-canceled')).toHaveText('1');await expect(part(page,'task-completed')).toHaveText('0');
  expect((await status(page)).tasksStarted).toBe(1);
  await expect.poll(async()=>Number(await part(page,'ticks').textContent())).toBeGreaterThan(ticks);
});

test('history restores focus and real window and nested scroll without reloading the document',async({page,native},info)=>{
  let saved,outgoing,afterBack,phaseProbe,phaseArmAttempted=false,originalAssertionAttempted=false;
  try{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'draft').fill('Focused draft');await part(page,'draft').focus();
  await page.evaluate(()=>{window.scrollTo(0,500);document.querySelector('[data-retained-scroll]').scrollTop=160;});
  await page.waitForFunction(()=>history.state?.nextgoY===window.scrollY&&window.scrollY>0);
  saved=await page.evaluate(()=>({window:scrollY,nested:document.querySelector('[data-retained-scroll]').scrollTop}));
  // Programmatic native click keeps the current input focus while exercising
  // the actual document link listener and Go router.
  outgoing=await page.evaluate(()=>{
    const read=()=>{
      try{
        const number=value=>({present:Number.isFinite(value),value:Number.isFinite(value)?value:0}),viewport=window.visualViewport;
        return {complete:true,windowX:number(scrollX),windowY:number(scrollY),documentY:number(document.scrollingElement?.scrollTop),historyX:number(history.state?.nextgoX),historyY:number(history.state?.nextgoY),viewportPresent:!!viewport,viewportLeft:number(viewport?.offsetLeft),viewportTop:number(viewport?.offsetTop),viewportPageLeft:number(viewport?.pageLeft),viewportPageTop:number(viewport?.pageTop),viewportWidth:number(viewport?.width),viewportHeight:number(viewport?.height),viewportScale:number(viewport?.scale),focusedDraft:document.activeElement===retainedA.draft,sameDocument:document===retainedDocument,pathA:location.pathname==='/a',pathB:location.pathname==='/b',hydrationReady:document.body.getAttribute('data-nextgo-hydration')==='ready',appBusy:document.getElementById('nextgo-app').hasAttribute('aria-busy'),ownerHidden:retainedA.owner.hidden,ownerConnected:retainedA.owner.isConnected};
      }catch{return {complete:false};}
    };
    const before=read();document.querySelector('nav a[href="/b"]').click();return {before,after:read()};
  });await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','B');
  // Source-admitted diagnostic: arm only on B, immediately before expected back.
  // Native wrappers and selection IDL probes may change scheduling; a pass is not
  // a bug-fix result. Setup/collection failure must preserve the original test.
  phaseArmAttempted=true;
  try{phaseProbe=await armRetainedScrollProbe(page);}catch{}
  await backA(page);await expect(part(page,'draft')).toBeFocused();
  const restored=await page.evaluate(()=>{
    // Sample the original assertion's exact fields FIRST, before diagnostics.
    const actual={window:scrollY,nested:document.querySelector('[data-nextgo-view-active] [data-retained-scroll]').scrollTop,same:document===retainedDocument};
    let observation;
    try{
      const number=value=>({present:Number.isFinite(value),value:Number.isFinite(value)?value:0}),viewport=window.visualViewport;
      observation={complete:true,windowX:number(scrollX),windowY:number(scrollY),documentY:number(document.scrollingElement?.scrollTop),historyX:number(history.state?.nextgoX),historyY:number(history.state?.nextgoY),viewportPresent:!!viewport,viewportLeft:number(viewport?.offsetLeft),viewportTop:number(viewport?.offsetTop),viewportPageLeft:number(viewport?.pageLeft),viewportPageTop:number(viewport?.pageTop),viewportWidth:number(viewport?.width),viewportHeight:number(viewport?.height),viewportScale:number(viewport?.scale),focusedDraft:document.activeElement===retainedA.draft,sameDocument:document===retainedDocument,pathA:location.pathname==='/a',pathB:location.pathname==='/b',hydrationReady:document.body.getAttribute('data-nextgo-hydration')==='ready',appBusy:document.getElementById('nextgo-app').hasAttribute('aria-busy'),ownerHidden:retainedA.owner.hidden,ownerConnected:retainedA.owner.isConnected};
    }catch{observation={complete:false};}
    return {actual,observation};
  });
  afterBack=restored.observation;
  originalAssertionAttempted=true;
  expect(restored.actual).toEqual({...saved,same:true});
  // This fixture has no application status styles: loading must not move it.
  expect(outgoing.before.complete && outgoing.after.complete).toBe(true);
  expect(outgoing.after.windowY).toEqual(outgoing.before.windowY);
  expect(outgoing.after.documentY).toEqual(outgoing.before.documentY);
  }finally{
    // Collect only after the original assertion attempt (or an earlier original
    // failure). No geometry reads, extra focus/scroll calls, waits, or retries occur.
    let phase;
    try{phase=await collectRetainedScrollProbe(page,phaseProbe,phaseArmAttempted);}
    catch{phase={version:1,observed:false,sameDocument:null,cleanup:{attempted:null},collectionFailed:true};}
    // Attachment happens after the original assertion attempt. Missing values
    // stay unknown; a later historyY is not the popstate event's saved value.
    try{
      const proof={version:1,savedCaptured:!!saved,outgoingCaptured:!!outgoing,afterBackCaptured:!!afterBack,...(saved?{saved}:{}),...(outgoing?{outgoing}:{}),...(afterBack?{afterBack}:{})};
      const body=Buffer.from(JSON.stringify(proof));
      if(body.length<=4096)await info.attach('retained-scroll-observations',{body,contentType:'application/json'});
      else console.warn('Retained scroll diagnostics incomplete: attachment byte cap');
    }catch{console.warn('Retained scroll diagnostics incomplete: attachment failed');}
    try{
      const proof={version:1,phaseArmAttempted,originalAssertionAttempted,phase};
      const body=Buffer.from(JSON.stringify(proof));
      if(body.length<=8192)await info.attach('retained-scroll-phase-observations',{body,contentType:'application/json'});
      else console.warn('Retained scroll phase diagnostics incomplete: attachment byte cap');
    }catch{console.warn('Retained scroll phase diagnostics incomplete: attachment failed');}
  }
});

test('changed server output takes a fresh render instead of serving retained stale HTML',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'increment').click();await go(page,'B');await control(page,{version:'2'});
  await backA(page);await expect(page.getByRole('heading',{level:1})).toHaveText('Page A · version 2');await expect(part(page,'count')).toHaveText('2');
  expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
});

test('a fresh privacy partition and a denied response never reveal an older draft',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'draft').fill('Old partition draft');await go(page,'B');await control(page,{partition:'2'});
  await backA(page);await expect(part(page,'draft')).toHaveValue('');expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
  await saveA(page);await go(page,'B');await control(page,{deny:true});await page.goBack();
  await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(panel(page)).toHaveCount(0);
  expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
});

test('the three-view bound evicts the oldest owner and unsupported author IDs take the cold path',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'draft').fill('Evicted draft');
  await go(page,'B');await go(page,'C');await go(page,'D');expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
  expect(await page.locator('#nextgo-app > main').count()).toBeLessThanOrEqual(3);
  await page.goBack();await ready(page);await page.goBack();await ready(page);await backA(page);await expect(part(page,'draft')).toHaveValue('');
  await saveA(page);await go(page,'IDS');await expect(page.getByLabel('Cold path with an author ID')).toBeVisible();await backA(page);
  expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
});


test('a superseded request cannot activate a stale view or add a ghost history entry',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'increment').click();
  const pending=await holdPage(page,'/b');
  try{
    await page.getByRole('navigation',{name:'Retained example'}).getByRole('link',{name:'B',exact:true}).click();
    await pending.waitForEntry();
    await go(page,'C');pending.release();await pending.close();
    await expect(page).toHaveURL(native.origin+'/c');await expect(panel(page)).toHaveAttribute('data-retained-panel','C');
    expect(await page.evaluate(()=>document===retainedDocument)).toBe(true);
    await backA(page);await expect(part(page,'count')).toHaveText('3');
    expect(await page.evaluate(()=>retainedA.root===document.querySelector('[data-nextgo-view-active] [data-retained-panel]'))).toBe(true);
  }finally{await pending.close();}
});

for(const kind of ['details','anchor','radio','form','form-association']){
  test(`document-wide native ownership ${kind} bypasses retained activation`,async({page,native})=>{
    await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'draft').fill('Must be evicted');
    await page.getByRole('navigation',{name:'Retained example'}).getByRole('link',{name:'UNSAFE '+kind,exact:true}).click();await ready(page);
    await expect(panel(page)).toHaveAttribute('data-retained-panel','UNSAFE');await backA(page);
    await expect(part(page,'draft')).toHaveValue('');expect(await page.evaluate(()=>!retainedA.root.isConnected)).toBe(true);
  });
}

test('a sole eager island owner retains its own identity state and scroll container',async({page,native},info)=>{
  let savedActual,afterBackActual,savedObservation,outgoingObservation,afterBackObservation;
  try{
  await page.goto(native.origin+'/owner');await ready(page);
  await expect(page.locator('#nextgo-app > [data-nextgo-island="retentionpanel"]')).toHaveAttribute('data-nextgo-hydrated','true');
  await part(page,'increment').click();
  const sampled=await page.evaluate(()=>{
    const owner=document.querySelector('#nextgo-app > [data-nextgo-island]'),draft=owner.querySelector('[data-retained-draft]');
    globalThis.retainedOwner=owner;globalThis.ownerDocument=document;
    // Establish native draft/focus/scroll state without an earlier fill action's
    // caret-reveal work. Capture and dispatch the eligible anchor in this same
    // synchronous task, so observation and native dispatch cannot drift between tasks.
    draft.value='Owner island draft';draft.focus({preventScroll:true});owner.scrollTop=200;
    // Sample the original actual scroll field before any diagnostic reads.
    const actual=owner.scrollTop;
    const read=()=>{try{const number=value=>({present:Number.isFinite(value),value:Number.isFinite(value)?value:0}),owner=retainedOwner,draft=owner.querySelector('[data-retained-draft]');return {complete:true,ownerX:number(owner.scrollLeft),ownerY:number(owner.scrollTop),windowX:number(scrollX),windowY:number(scrollY),documentY:number(document.scrollingElement?.scrollTop),historyX:number(history.state?.nextgoX),historyY:number(history.state?.nextgoY),selectionStart:number(draft?.selectionStart),selectionEnd:number(draft?.selectionEnd),selectionForward:draft?.selectionDirection==='forward',selectionBackward:draft?.selectionDirection==='backward',selectionNone:draft?.selectionDirection==='none',focusedDraft:document.activeElement===draft,sameDocument:document===ownerDocument,ownerHidden:owner.hidden,ownerConnected:owner.isConnected,ownerActive:owner.hasAttribute('data-nextgo-view-active'),pathOwner:location.pathname==='/owner',pathB:location.pathname==='/b',hydrationReady:document.body.getAttribute('data-nextgo-hydration')==='ready',appBusy:document.getElementById('nextgo-app').hasAttribute('aria-busy')};}catch{return {complete:false};}};
    const before=read();owner.querySelector('a[href="/b"]').click();
    return {actual,observation:before,outgoing:{before,after:read()}};
  });const scroll=sampled.actual;savedActual=scroll;savedObservation=sampled.observation;outgoingObservation=sampled.outgoing;
  expect(scroll).toBe(200);expect(savedObservation.focusedDraft).toBe(true);
  expect(outgoingObservation.before.complete&&outgoingObservation.after.complete).toBe(true);
  expect(outgoingObservation.before.ownerY).toEqual({present:true,value:200});
  expect(outgoingObservation.after.ownerY).toEqual(outgoingObservation.before.ownerY);
  await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','B');
  await page.goBack();await ready(page);await expect(panel(page)).toHaveAttribute('data-retained-panel','OWNER');
  expect(await page.evaluate(()=>retainedOwner===document.querySelector('#nextgo-app > [data-nextgo-view-active]')&&document===ownerDocument)).toBe(true);
  const restored=await page.evaluate(()=>{
    // Sample the original actual scroll field before any diagnostic reads.
    const actual=retainedOwner.scrollTop;
    const read=()=>{try{const number=value=>({present:Number.isFinite(value),value:Number.isFinite(value)?value:0}),owner=retainedOwner,draft=owner.querySelector('[data-retained-draft]');return {complete:true,ownerX:number(owner.scrollLeft),ownerY:number(owner.scrollTop),windowX:number(scrollX),windowY:number(scrollY),documentY:number(document.scrollingElement?.scrollTop),historyX:number(history.state?.nextgoX),historyY:number(history.state?.nextgoY),selectionStart:number(draft?.selectionStart),selectionEnd:number(draft?.selectionEnd),selectionForward:draft?.selectionDirection==='forward',selectionBackward:draft?.selectionDirection==='backward',selectionNone:draft?.selectionDirection==='none',focusedDraft:document.activeElement===draft,sameDocument:document===ownerDocument,ownerHidden:owner.hidden,ownerConnected:owner.isConnected,ownerActive:owner.hasAttribute('data-nextgo-view-active'),pathOwner:location.pathname==='/owner',pathB:location.pathname==='/b',hydrationReady:document.body.getAttribute('data-nextgo-hydration')==='ready',appBusy:document.getElementById('nextgo-app').hasAttribute('aria-busy')};}catch{return {complete:false};}};
    return {actual,observation:read()};
  });afterBackActual=restored.actual;afterBackObservation=restored.observation;
  expect(restored.actual).toBe(scroll);expect(afterBackObservation.focusedDraft).toBe(true);await expect(part(page,'count')).toHaveText('3');await expect(part(page,'draft')).toHaveValue('Owner island draft');
  }finally{
    // Fixed scalar observations do not reveal component text, props or URLs.
    // Outgoing commit-time capture is unobserved; these are click/back samples.
    try{
      const proof={version:1,savedActualCaptured:Number.isFinite(savedActual),afterBackActualCaptured:Number.isFinite(afterBackActual),savedCaptured:!!savedObservation,outgoingCaptured:!!outgoingObservation,afterBackCaptured:!!afterBackObservation,...(Number.isFinite(savedActual)?{savedActual}:{}),...(Number.isFinite(afterBackActual)?{afterBackActual}:{}),...(savedObservation?{saved:savedObservation}:{}),...(outgoingObservation?{outgoing:outgoingObservation}:{}),...(afterBackObservation?{afterBack:afterBackObservation}:{})};
      const body=Buffer.from(JSON.stringify(proof));
      if(body.length<=4096)await info.attach('retained-owner-scroll-observations',{body,contentType:'application/json'});
      else console.warn('Retained owner scroll diagnostics incomplete: attachment byte cap');
    }catch{console.warn('Retained owner scroll diagnostics incomplete: attachment failed');}
  }
});

test('a mount failure on the sole eager owner is reported and cannot become a retained view',async({page,native})=>{
  await page.goto(native.origin+'/broken');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');
  await expect(page.locator('#nextgo-app > [data-nextgo-island="retentionpanel"]')).toHaveAttribute('data-nextgo-hydrated','error');
  await expect(page.locator('#nextgo-status')).toContainText('deliberate direct-owner mount failure');
  await page.evaluate(()=>{globalThis.brokenOwner=document.querySelector('#nextgo-app > [data-nextgo-island]');globalThis.brokenDocument=document;});
  await go(page,'B');await page.goBack();await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');
  expect(await page.evaluate(()=>!brokenOwner.isConnected&&brokenOwner!==document.querySelector('#nextgo-app > [data-nextgo-island]')&&document===brokenDocument)).toBe(true);
});

test('same-path query navigation moves focus out of the connected hidden previous visit',async({page,native})=>{
  await page.goto(native.origin+'/a');await ready(page);await saveA(page);await part(page,'draft').fill('Previous visit draft');await part(page,'draft').focus();
  const before=(await status(page)).pageLoads;
  // Change a real eligible native anchor and click without moving focus first.
  // A query push creates a fresh visit in this slice, even for identical HTML.
  await page.evaluate(()=>{const link=retainedA.owner.querySelector('nav a[href="/a"]');link.setAttribute('href','/a?focus=1');link.click();});
  await expect(page).toHaveURL(native.origin+'/a?focus=1');await ready(page);
  await expect(page.locator('#nextgo-app > main[data-nextgo-view-active]')).toBeFocused();
  expect(await page.evaluate(()=>{const current=document.querySelector('#nextgo-app > main[data-nextgo-view-active]');return document===retainedDocument&&current!==retainedA.owner&&retainedA.owner.isConnected&&retainedA.owner.hidden&&retainedA.owner.hasAttribute('inert')&&!retainedA.owner.contains(document.activeElement)&&current===document.activeElement;})).toBe(true);
  expect((await status(page)).pageLoads).toBeGreaterThan(before);await expect(part(page,'draft')).toHaveValue('');await expect(part(page,'count')).toHaveText('2');
});

test('newer network failure from retired cleanup preserves the already hydrated committed page',async({page,native})=>{
  await page.goto(native.origin+'/a?retire=1');await ready(page);await saveA(page);
  const newer=await holdRequests(page,'**/c?retired=1',request=>request.headers()['x-nextgo-navigate']==='1'&&request.headers()['x-nextgo-purpose']!=='prefetch',true);
  try{
    // A fresh partition denies retention of old A and runs its terminal cleanup.
    await control(page,{partition:'2'});await go(page,'B');await newer.waitForEntry();
    await expect(page).toHaveURL(native.origin+'/b');await expect(page).toHaveTitle('Retained page B');
    await expect(panel(page).locator('xpath=..')).toHaveAttribute('data-nextgo-hydrated','true');
    await expect(part(page,'increment')).toBeEnabled();await part(page,'increment').click();await expect(part(page,'count')).toHaveText('3');
    expect(await page.evaluate(()=>document===retainedDocument&&!retainedA.owner.isConnected&&document.querySelector('#nextgo-app > main[data-nextgo-view-active]')?.querySelector('[data-retained-panel]')?.getAttribute('data-retained-panel')==='B')).toBe(true);
    newer.release();await newer.close();
    await expect(page.locator('#nextgo-status')).toContainText('Navigation failed. Try again or reload.');
    await ready(page);await expect(page).toHaveURL(native.origin+'/b');await expect(page).toHaveTitle('Retained page B');
    await expect(panel(page)).toHaveAttribute('data-retained-panel','B');await part(page,'increment').click();await expect(part(page,'count')).toHaveText('4');
    expect(await page.evaluate(()=>document===retainedDocument&&!retainedA.owner.isConnected)).toBe(true);
    await go(page,'A');await expect(page).toHaveURL(native.origin+'/a');await expect(part(page,'count')).toHaveText('2');
  }finally{await newer.close();}
});

test('failed retained effect resume stays visibly failed with disposed listeners and permits fresh navigation recovery',async({page,native})=>{
  await page.goto(native.origin+'/a?resumeFail=1');await ready(page);await saveA(page);await part(page,'increment').click();await go(page,'B');
  await page.goBack();await expect(page).toHaveURL(native.origin+'/a?resumeFail=1');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');
  await expect(panel(page).locator('xpath=..')).toHaveAttribute('data-nextgo-hydrated','error');
  await expect(page.locator('#nextgo-status')).toHaveText('A component could not resume. Navigate to another page or reload.');
  await expect(part(page,'count')).toHaveText('3');await part(page,'increment').click();await expect(part(page,'count')).toHaveText('3');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');
  expect(await page.evaluate(()=>document===retainedDocument&&retainedA.owner===document.querySelector('#nextgo-app > main[data-nextgo-view-active]'))).toBe(true);
  await go(page,'B');await go(page,'A');await expect(page).toHaveURL(native.origin+'/a');await expect(part(page,'count')).toHaveText('2');
  await part(page,'increment').click();await expect(part(page,'count')).toHaveText('3');
  expect(await page.evaluate(()=>document===retainedDocument&&!retainedA.owner.isConnected)).toBe(true);
});

test('a terminal cleanup panic cannot veto an authorized unsupported cold destination',async({page,native})=>{
  await page.goto(native.origin+'/a?cleanupPanic=1');await ready(page);await saveA(page);
  await go(page,'IDS');await expect(page).toHaveURL(native.origin+'/ids');await expect(page).toHaveTitle('Retained page IDS');
  await expect(page.getByLabel('Cold path with an author ID')).toBeVisible();
  await expect(panel(page).locator('xpath=..')).toHaveAttribute('data-nextgo-hydrated','true');
  await expect(page.locator('#nextgo-status')).toHaveText('A component could not finish closing. Navigate to another page or reload.');
  await part(page,'increment').click();await expect(part(page,'count')).toHaveText('3');await ready(page);
  expect(await page.evaluate(()=>document===retainedDocument&&!retainedA.owner.isConnected&&document.querySelector('#nextgo-app > main')?.querySelector('[data-retained-panel]')?.getAttribute('data-retained-panel')==='IDS')).toBe(true);
  await expect(page.locator('#nextgo-status')).toHaveText('A component could not finish closing. Navigate to another page or reload.');
  await go(page,'A');await expect(page).toHaveURL(native.origin+'/a');await expect(page).toHaveTitle('Retained page A');
  await expect(part(page,'count')).toHaveText('2');await part(page,'increment').click();await expect(part(page,'count')).toHaveText('3');
  expect(await page.evaluate(()=>document===retainedDocument&&!retainedA.owner.isConnected)).toBe(true);
});
