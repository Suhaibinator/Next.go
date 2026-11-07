// Pure qualification target audit; no production runtime code or cross-emulation.
const targets=Object.freeze({
  amd64:Object.freeze({goArch:'amd64',nodeArch:'x64',elfMachine:'3e00'}),
  arm64:Object.freeze({goArch:'arm64',nodeArch:'arm64',elfMachine:'b700'}),
});
function require(value,message){if(!value)throw Error(message);}
export function nativeTarget(selected,platform,nodeArch){
  require(Object.hasOwn(targets,selected),'explicit amd64 or arm64 native target required');
  const target=targets[selected];
  require(platform==='linux'&&nodeArch===target.nodeArch,'Node host does not match selected native Linux target');
  return target;
}
export function nativeToolPaths(target){
  require(Object.values(targets).includes(target),'audited native target required');
  return ['bin/go','bin/gofmt',...['compile','link','vet'].map(name=>'pkg/tool/linux_'+target.goArch+'/'+name),'lib/wasm/go_js_wasm_exec','lib/wasm/wasm_exec_node.js','lib/wasm/wasm_exec.js'];
}
export function assertNativeELF(header,target){
  require(Object.values(targets).includes(target),'audited native target required');
  require(header instanceof Uint8Array&&header.length>=20,'bounded ELF header required');
  const hex=Buffer.from(header.subarray(0,20)).toString('hex');
  require(hex.startsWith('7f454c460201')&&hex.slice(36,40)===target.elfMachine,'ELF64 little-endian machine does not match native target');
}
export function assertNativeGoEnvironment(env,target){
  require(Object.values(targets).includes(target),'audited native target required');
  require(env.GOOS==='linux'&&env.GOARCH===target.goArch&&env.GOHOSTOS==='linux'&&env.GOHOSTARCH===target.goArch,'Go host/target mismatch; cross-compilation is not native qualification');
}
export function assertNativeBuildInfo(text,path,version,target){
  require(Object.values(targets).includes(target),'audited native target required');
  const lines=text.split('\n'),header=path+': go'+version,records=lines.flatMap((line,index)=>line===header?[index]:[]);
  require(records.length===1,'one exact selected-SDK executable record required');
  const section=[];for(let index=records[0]+1;index<lines.length&&lines[index].startsWith('\t');index++)section.push(lines[index]);
  for(const [key,value] of [['GOOS','linux'],['GOARCH',target.goArch],['CGO_ENABLED','1']]){
    const settings=section.filter(line=>line.startsWith('\tbuild\t'+key+'='));
    require(settings.length===1&&settings[0]==='\tbuild\t'+key+'='+value,'one exact production build setting required: '+key);
  }
}
