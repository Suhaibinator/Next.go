// Native-browser diagnostics for one pinned plain-input retained history test.
// This changes wrapper identity and executes extra native selection IDL reads.
// It adds no focus/scroll calls, geometry reads, timers, retries, or app behavior.
// A passing instrumented test is not evidence that the underlying bug is fixed.
export async function armRetainedScrollProbe(page){
  return page.evaluateHandle(()=>{
    const originalDocument=document,apply=Reflect.apply,own=Object.getOwnPropertyDescriptor,hasOwn=Object.prototype.hasOwnProperty;
    const define=Object.defineProperty,prototype=Object.getPrototypeOf,remove=Reflect.deleteProperty;
    const roles=[],records=[],counts={pop:0,scroll:0,replace:0,ownerFocus:0,draftFocus:0,selection:0};
    const flags={truncated:false,saturated:false,observerErrors:0,nonData:false,selectionUnavailable:false,setupError:false,unowned:false};
    let sequence=0,dropped=0,lastReplace=null,setupComplete=false,finished=false,listener=null,listenerAdded=false,cleanupResult=null;
    const increment=value=>{if(value>=32){flags.saturated=true;return 32;}return value+1;};
    const observe=operation=>{try{return operation();}catch{flags.observerErrors=increment(flags.observerErrors);return null;}};
    const owns=(object,key)=>apply(hasOwn,object,[key]);
    const finite=value=>typeof value==='number'&&Number.isFinite(value)?value:null;
    const direction=value=>value==='none'||value==='forward'||value==='backward'?value:null;
    const data=(object,key)=>{
      if((typeof object!=='object'||object===null)&&typeof object!=='function')return {known:false,present:false,value:null};
      const descriptor=own(object,key);
      if(!descriptor)return {known:true,present:false,value:null};
      if(!owns(descriptor,'value')){flags.nonData=true;return {known:false,present:true,value:null};}
      return {known:true,present:true,value:descriptor.value};
    };
    const stateFields=state=>{
      const x=data(state,'nextgoX'),y=data(state,'nextgoY'),entry=data(state,'nextgoEntry');
      return {x:x.known&&x.present?finite(x.value):null,y:y.known&&y.present?finite(y.value):null,entry:entry.known?(entry.present&&typeof entry.value==='string'&&entry.value.length>0):null};
    };
    const record=value=>{
      sequence=increment(sequence);
      if(records.length<16)records.push({n:sequence,...value});
      else{flags.truncated=true;dropped=increment(dropped);}
    };
    const resolve=(object,key)=>{
      let cursor=object;
      for(let depth=0;cursor!==null&&depth<8;depth++,cursor=prototype(cursor)){
        const descriptor=own(cursor,key);
        if(descriptor)return descriptor;
      }
      return null;
    };
    const nativeMethod=(object,key)=>{
      const descriptor=resolve(object,key);
      if(!descriptor||!owns(descriptor,'value')||typeof descriptor.value!=='function')throw null;
      return descriptor.value;
    };
    const sameDescriptor=(left,right)=>{
      if(!left||!right)return left===right;
      for(const key of ['value','get','set','writable','enumerable','configurable']){
        if(owns(left,key)!==owns(right,key)||left[key]!==right[key])return false;
      }
      return true;
    };
    let removeListener=null,readEventState=null,readSelection=null;
    const cleanup=()=>{
      const result={attempted:true,listener:null,roles:[]};
      // Removing this exact listener is safe even after partial registration.
      if(listener&&removeListener){
        try{apply(removeListener,window,['popstate',listener,true]);result.listener=true;}
        catch{result.listener=false;flags.observerErrors=increment(flags.observerErrors);}
      }
      for(let index=roles.length-1;index>=0;index--){
        const role=roles[index];
        if(!role.installed){result.roles.push({role:role.label,status:'not-installed'});continue;}
        try{
          const live=own(role.target,role.key);
          if(!live||!owns(live,'value')||live.value!==role.wrapper){
            flags.unowned=true;result.roles.push({role:role.label,status:'unowned'});continue;
          }
          if(role.original)define(role.target,role.key,role.original);
          else if(!remove(role.target,role.key))throw null;
          const restored=own(role.target,role.key);
          result.roles.push({role:role.label,status:sameDescriptor(restored,role.original)?'restored':'unverified'});
        }catch{flags.observerErrors=increment(flags.observerErrors);result.roles.push({role:role.label,status:'error'});}
      }
      return result;
    };
    const argsFor=(label,args)=>{
      const argc=Math.min(args.length,32);if(args.length>32)flags.saturated=true;
      if(label==='scroll'){
        if(typeof args[0]==='number')return {argc,x:finite(args[0]),y:finite(args[1])};
        const x=data(args[0],'left'),y=data(args[0],'top');
        return {argc,x:x.known&&x.present?finite(x.value):null,y:y.known&&y.present?finite(y.value):null};
      }
      if(label==='replace')return {argc,...stateFields(args[0])};
      if(label==='selection')return {argc,start:finite(args[0]),end:finite(args[1]),direction:direction(args[2])};
      const prevent=data(args[0],'preventScroll');
      return {argc,preventScroll:prevent.known&&prevent.present&&typeof prevent.value==='boolean'?prevent.value:null};
    };
    const install=(target,key,label)=>{
      const original=own(target,key),native=nativeMethod(target,key);
      if(original&&!original.configurable)throw null;
      const role={target,key,label,original,wrapper:null,installed:false};
      const wrapper=function(...args){
        let returned=false,replaceObservation=null;
        observe(()=>{
          counts[label]=increment(counts[label]);
          const fields=argsFor(label,args);
          if(label==='replace'){replaceObservation={...fields,returned:null};lastReplace=replaceObservation;}
          const selection=(label==='draftFocus'||label==='selection')&&readSelection?readSelection():null;
          record({role:label,phase:'before',...fields,...((label==='draftFocus'||label==='selection')?{selection}:{} )});
        });
        try{const value=apply(native,this,args);returned=true;return value;}
        finally{
          observe(()=>{
            if(replaceObservation)replaceObservation.returned=returned;
            const selection=(label==='draftFocus'||label==='selection')&&readSelection?readSelection():null;
            record({role:label,phase:'after',returned,...((label==='draftFocus'||label==='selection')?{selection}:{} )});
          });
        }
      };
      role.wrapper=wrapper;roles.push(role);
      define(target,key,{value:wrapper,writable:true,configurable:true,enumerable:original?.enumerable??false});
      role.installed=true;
    };
    try{
      const draft=retainedA.draft,owner=retainedA.owner;
      if(!(draft instanceof HTMLInputElement))throw null;
      const getters=['selectionStart','selectionEnd','selectionDirection'].map(key=>resolve(draft,key)?.get);
      if(getters.some(getter=>typeof getter!=='function'))flags.selectionUnavailable=true;
      else readSelection=()=>({start:finite(apply(getters[0],draft,[])),end:finite(apply(getters[1],draft,[])),direction:direction(apply(getters[2],draft,[]))});
      const stateGetter=resolve(PopStateEvent.prototype,'state')?.get;
      if(typeof stateGetter!=='function')throw null;
      readEventState=event=>apply(stateGetter,event,[]);
      const addListener=nativeMethod(window,'addEventListener');
      removeListener=nativeMethod(window,'removeEventListener');
      install(window,'scrollTo','scroll');
      install(history,'replaceState','replace');
      install(owner,'focus','ownerFocus');
      install(draft,'focus','draftFocus');
      install(draft,'setSelectionRange','selection');
      listener=event=>observe(()=>{counts.pop=increment(counts.pop);record({role:'pop',phase:'event',...stateFields(readEventState(event))});});
      apply(addListener,window,['popstate',listener,true]);listenerAdded=true;
      setupComplete=true;
    }catch{
      flags.setupError=true;
      cleanupResult=cleanup();
    }
    return {
      finish(activeDocument){
        // Continuity is an authority fence: do not touch old targets or expose
        // old observations after a document replacement. Cleanup is unknown.
        if(activeDocument!==originalDocument)return {version:1,observed:false,sameDocument:false,documentChanged:true,cleanup:{attempted:null}};
        const repeated=finished;finished=true;
        if(!cleanupResult)cleanupResult=cleanup();
        return {version:1,observed:true,sameDocument:activeDocument===originalDocument,repeated,setupComplete,listenerAdded,sequence,dropped,records,counts,lastReplace,flags,cleanup:cleanupResult};
      },
    };
  });
}

export async function collectRetainedScrollProbe(page,probe,armAttempted=null){
  // A rejected evaluateHandle can leave setup/cleanup unobserved. Only an
  // explicitly never-attempted arm proves that cleanup was not attempted.
  if(!probe)return {version:1,observed:false,sameDocument:null,cleanup:{attempted:armAttempted===false?false:null},handleDisposed:null};
  let result;
  try{result=await page.evaluate(handle=>handle.finish(document),probe);}
  catch{
    let sameDocument=null;
    try{sameDocument=await page.evaluate(()=>typeof retainedDocument==='object'&&document===retainedDocument);}catch{}
    // Context loss/new-document or an unobserved failure cannot prove cleanup.
    result={version:1,observed:false,sameDocument,contextUnavailable:true,cleanup:{attempted:null}};
  }
  try{await probe.dispose();result.handleDisposed=true;}catch{result.handleDisposed=false;}
  return result;
}
