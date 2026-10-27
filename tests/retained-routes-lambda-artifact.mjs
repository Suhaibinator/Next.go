// Qualification-only reader for the CLI's actual one-entry Lambda ZIP.
import {inflateRawSync} from 'node:zlib';
import {mkdir,writeFile,lstat,open,realpath} from 'node:fs/promises';
import {constants} from 'node:fs';
import {createHash} from 'node:crypto';
import {join,dirname,isAbsolute,resolve} from 'node:path';
import {check,sha,readBounded,json} from './static-shell-ci-build.mjs';
import {nativeTarget,assertNativeELF} from './retained-routes-target.mjs';

export const lambdaFixtureLimits=Object.freeze({zip:50<<20,bootstrap:128<<20,manifest:1<<20,body:4<<20,envelope:6<<20});
export function crc32(bytes){let value=0xffffffff;for(const byte of bytes){value^=byte;for(let bit=0;bit<8;bit++)value=(value>>>1)^((value&1)?0xedb88320:0);}return(value^0xffffffff)>>>0;}
export function decodeBootstrapZIP(zip,expected){
  check(Buffer.isBuffer(zip)&&zip.length>=22&&zip.length<=lambdaFixtureLimits.zip,'bounded actual ZIP required');
  const end=zip.length-22;check(zip.readUInt32LE(end)===0x06054b50&&zip.readUInt16LE(end+20)===0,'one-disk comment-free CLI ZIP required');
  check(zip.readUInt16LE(end+4)===0&&zip.readUInt16LE(end+6)===0&&zip.readUInt16LE(end+8)===1&&zip.readUInt16LE(end+10)===1,'one actual ZIP entry required');
  const size=zip.readUInt32LE(end+12),central=zip.readUInt32LE(end+16);check(central>=30&&central+size===end&&size>=46,'bounded ZIP central directory');
  check(zip.readUInt32LE(central)===0x02014b50,'central ZIP signature');
  const flags=zip.readUInt16LE(central+8),method=zip.readUInt16LE(central+10),crc=zip.readUInt32LE(central+16),compressed=zip.readUInt32LE(central+20),bytes=zip.readUInt32LE(central+24),nameLength=zip.readUInt16LE(central+28),extra=zip.readUInt16LE(central+30),comment=zip.readUInt16LE(central+32),offset=zip.readUInt32LE(central+42),mode=zip.readUInt32LE(central+38)>>>16;
  check((flags&~0x808)===0&&method===8&&offset===0&&zip.readUInt16LE(central+34)===0,'unencrypted sole deflate entry at offset0 required');
  check(nameLength===9&&extra<=256&&comment===0&&46+nameLength+extra===size&&zip.subarray(central+46,central+55).toString('ascii')==='bootstrap','literal bootstrap name only');
  check((mode&0xf000)===0x8000&&(mode&0xfff)===0o755&&bytes===expected.bytes&&bytes>0&&bytes<=lambdaFixtureLimits.bootstrap,'regular executable bootstrap fixture size/mode');
  check(zip.readUInt32LE(0)===0x04034b50&&zip.readUInt16LE(6)===flags&&zip.readUInt16LE(8)===method&&zip.readUInt16LE(26)===9,'local entry matches central directory');
  const localExtra=zip.readUInt16LE(28),start=30+9+localExtra,finish=start+compressed;check(localExtra<=256&&zip.subarray(30,39).toString('ascii')==='bootstrap'&&finish<=central,'bounded literal local ZIP entry');
  if(flags&8){let pos=finish;if(zip.readUInt32LE(pos)===0x08074b50)pos+=4;check(pos+12===central&&zip.readUInt32LE(pos)===crc&&zip.readUInt32LE(pos+4)===compressed&&zip.readUInt32LE(pos+8)===bytes,'data descriptor matches actual central entry');}
  else check(finish===central&&zip.readUInt32LE(14)===crc&&zip.readUInt32LE(18)===compressed&&zip.readUInt32LE(22)===bytes,'local ZIP lengths/CRC match');
  const body=inflateRawSync(zip.subarray(start,finish),{maxOutputLength:lambdaFixtureLimits.bootstrap});
  check(body.length===bytes&&crc32(body)===crc&&sha(body)===expected.sha256,'actual extracted bootstrap digest/CRC/size mismatch');return body;
}
export function assertStaticBootstrap(bytes,arch){
  const target=nativeTarget(arch,'linux',arch==='amd64'?'x64':'arm64');assertNativeELF(bytes,target);check(bytes.length>=64&&bytes.readUInt16LE(16)===2,'static Go executable ELF header required');
  const entry=bytes.readBigUInt64LE(24),offset=bytes.readBigUInt64LE(32),width=bytes.readUInt16LE(54),count=bytes.readUInt16LE(56);check(offset<=BigInt(bytes.length)&&width===56&&count>0&&count<=128&&offset+BigInt(width*count)<=BigInt(bytes.length),'bounded actual ELF program table');
  let executable=false;for(let i=0;i<count;i++){const p=Number(offset)+i*width,type=bytes.readUInt32LE(p),flags=bytes.readUInt32LE(p+4),address=bytes.readBigUInt64LE(p+16),memory=bytes.readBigUInt64LE(p+40);check(type!==3&&type!==2,'fixture requires static ELF without interpreter/dynamic table');if(type===1&&(flags&1)&&entry>=address&&entry<address+memory)executable=true;}check(executable,'ELF entry in executable LOAD required');
}
export function validateLambdaManifest(metadata,{arch,goVersion}){
  const expected={amd64:{architecture:'x86_64',isa:'GOAMD64=v1',machine:'EM_X86_64'},arm64:{architecture:'arm64',isa:'GOARM64=v8.0',machine:'EM_AARCH64'}}[arch];
  check(expected&&metadata.format==='nextgo-lambda-v1'&&metadata.runtime==='provided.al2023'&&metadata.handler==='bootstrap'&&metadata.goos==='linux'&&metadata.goarch===arch&&metadata.cgoEnabled==='0'&&metadata.architecture===expected.architecture&&metadata.isaBaseline===expected.isa&&metadata.elf?.machine===expected.machine&&metadata.elf.class==='ELFCLASS64'&&metadata.elf.type==='ET_EXEC'&&metadata.elf.static===true&&metadata.elf.goVersion==='go'+goVersion,'actual CLI deployment architecture/ISA/compiler contract');
  check(/^[a-f0-9]{20}$/.test(metadata.runtimeId||'')&&/^[a-f0-9]{64}$/.test(metadata.sourceFingerprint||''),'actual runtime/source fingerprint format');
  check(Array.isArray(metadata.assets)&&metadata.assets.length<=4096,'bounded actual embedded asset inventory');const paths=new Set(),requests=new Set();
  for(const asset of metadata.assets){check(typeof asset.embeddedPath==='string'&&asset.embeddedPath.length<=4096&&!asset.embeddedPath.includes('\\')&&!asset.embeddedPath.includes('\0')&&!isAbsolute(asset.embeddedPath)&&asset.embeddedPath.split('/').every(part=>part&&part!=='.'&&part!=='..')&&!paths.has(asset.embeddedPath),'unique canonical embedded asset path');paths.add(asset.embeddedPath);check(Number.isSafeInteger(asset.bytes)&&asset.bytes>=0&&asset.bytes<=lambdaFixtureLimits.bootstrap&&/^[a-f0-9]{64}$/.test(asset.sha256||''),'actual asset size/digest');if(asset.defaultRequestPath!==undefined){check(typeof asset.defaultRequestPath==='string'&&asset.defaultRequestPath.startsWith('/')&&!asset.defaultRequestPath.startsWith('//')&&!/[\\?#\0]/.test(asset.defaultRequestPath)&&asset.defaultRequestPath.length<=4096&&!requests.has(asset.defaultRequestPath),'literal unique default request path');requests.add(asset.defaultRequestPath);}}
}
export function assertLambdaBuildInfo(text,binary,{arch,goVersion}){
  const header=binary+': go'+goVersion,lines=text.split('\n'),matches=lines.flatMap((line,index)=>line===header?[index]:[]);check(matches.length===1,'one exact bootstrap Go build-info record');const section=[];for(let i=matches[0]+1;i<lines.length&&lines[i].startsWith('\t');i++)section.push(lines[i]);
  for(const [key,value]of [['GOOS','linux'],['GOARCH',arch],['CGO_ENABLED','0'],arch==='amd64'?['GOAMD64','v1']:['GOARM64','v8.0']]){const values=section.filter(line=>line.startsWith('\tbuild\t'+key+'='));check(values.length===1&&values[0]==='\tbuild\t'+key+'='+value,'actual static bootstrap Go build setting: '+key);}
}
export async function extractLambdaArtifact(directory,destination,{arch,goVersion}){
  const metadata=await json(join(directory,'deployment.json'),lambdaFixtureLimits.manifest),zip=await readBounded(join(directory,'function.zip'),lambdaFixtureLimits.zip);
  validateLambdaManifest(metadata,{arch,goVersion});
  check(metadata.bufferedTransport==='UNVERIFIED'&&metadata.bufferedBodyLimit===4<<20&&metadata.bufferedEnvelopeLimit===6<<20,'packaging does not imply transport qualification');
  check(metadata.archive?.path==='function.zip'&&metadata.archive.bytes===zip.length&&metadata.archive.sha256===sha(zip)&&metadata.bootstrap?.path==='bootstrap'&&/^[a-f0-9]{64}$/.test(metadata.bootstrap.sha256),'actual ZIP metadata digest');
  const bytes=decodeBootstrapZIP(zip,metadata.bootstrap);assertStaticBootstrap(bytes,arch);
  await mkdir(destination,{mode:0o700});const binary=join(destination,'bootstrap');await writeFile(binary,bytes,{mode:0o755,flag:'wx'});
  return {binary,sha256:sha(bytes),bytes:bytes.length,archive:{path:join(directory,'function.zip'),bytes:zip.length,sha256:sha(zip)},metadata:{path:join(directory,'deployment.json'),sha256:sha(await readBounded(join(directory,'deployment.json'),lambdaFixtureLimits.manifest))},runtimeId:metadata.runtimeId,sourceFingerprint:metadata.sourceFingerprint,assets:metadata.assets,SDK:goVersion,arch};
}

// Deliberately preserve the native fixture unchanged; same pin authority below.
export async function artifactHash(path) {
  const parent=dirname(path);
  if(!isAbsolute(path)||resolve(path)!==path||await realpath(parent)!==parent)throw Error('Retained producer parent must be canonical');
  const directory=await lstat(parent,{bigint:true}),owner=typeof process.getuid==='function'?process.getuid():null;
  if(!directory.isDirectory()||directory.isSymbolicLink()||(directory.mode&0o022n)!==0n||owner!==null&&directory.uid!==BigInt(owner))throw Error('Retained producer parent is not cooperatively owned');
  const file=await open(path,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
  try{
    const before=await file.stat({bigint:true});
    if(!before.isFile()||before.nlink!==1n||!(before.mode&0o111n)||(before.mode&0o022n)!==0n||before.size<1n||before.size>128n<<20n||owner!==null&&before.uid!==BigInt(owner))throw Error('Invalid retained example executable');
    const digest=createHash('sha256'),bytes=Buffer.alloc(64<<10);let count=0;
    for(;;){const part=await file.read(bytes,0,bytes.length,null);if(!part.bytesRead)break;count+=part.bytesRead;if(count>128<<20)throw Error('Executable size changed');digest.update(bytes.subarray(0,part.bytesRead));}
    const after=await file.stat({bigint:true}),afterDirectory=await lstat(parent,{bigint:true});
    if(BigInt(count)!==before.size||after.size!==before.size||after.dev!==before.dev||after.ino!==before.ino||after.mtimeNs!==before.mtimeNs||after.mode!==before.mode||after.uid!==before.uid||after.nlink!==before.nlink||afterDirectory.dev!==directory.dev||afterDirectory.ino!==directory.ino||afterDirectory.uid!==directory.uid||afterDirectory.mode!==directory.mode||await realpath(parent)!==parent)throw Error('Executable or producer parent changed while hashing');
    return {sha256:digest.digest('hex'),bytes:count,dev:before.dev.toString(),inode:before.ino.toString(),parent:{path:parent,dev:directory.dev.toString(),inode:directory.ino.toString()}};
  }finally{await file.close();}
}
