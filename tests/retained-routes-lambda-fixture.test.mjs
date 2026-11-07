// Synthetic ZIP/evidence negatives qualify auditors only, never an app artifact.
import test from 'node:test';
import assert from 'node:assert/strict';
import {deflateRawSync} from 'node:zlib';
import {createHash} from 'node:crypto';
import {crc32,decodeBootstrapZIP,assertStaticBootstrap,validateLambdaManifest,assertLambdaBuildInfo} from './retained-routes-lambda-artifact.mjs';
const sha=value=>createHash('sha256').update(value).digest('hex');
function archive(body){
  const packed=deflateRawSync(body),crc=crc32(body),name=Buffer.from('bootstrap'),local=Buffer.alloc(30),central=Buffer.alloc(46),end=Buffer.alloc(22);
  local.writeUInt32LE(0x04034b50);local.writeUInt16LE(20,4);local.writeUInt16LE(8,8);local.writeUInt32LE(crc,14);local.writeUInt32LE(packed.length,18);local.writeUInt32LE(body.length,22);local.writeUInt16LE(9,26);
  central.writeUInt32LE(0x02014b50);central.writeUInt16LE(0x314,4);central.writeUInt16LE(20,6);central.writeUInt16LE(8,10);central.writeUInt32LE(crc,16);central.writeUInt32LE(packed.length,20);central.writeUInt32LE(body.length,24);central.writeUInt16LE(9,28);central.writeUInt32LE((0o100755<<16)>>>0,38);
  end.writeUInt32LE(0x06054b50);end.writeUInt16LE(1,8);end.writeUInt16LE(1,10);end.writeUInt32LE(55,12);end.writeUInt32LE(local.length+name.length+packed.length,16);
  return Buffer.concat([local,name,packed,central,name,end]);
}
test('Lambda fixture extracts only one exact regular bootstrap with actual digest CRC and length',()=>{
  const body=Buffer.from('synthetic ZIP reader fixture'),zip=archive(body),expected={bytes:body.length,sha256:sha(body)};assert.deepEqual(decodeBootstrapZIP(zip,expected),body);
  for(const mutate of [b=>{b[b.length-12]=2;},b=>{b[30]=47;},b=>{b[b.length-22-55+46]=47;},b=>{b[b.length-22-55+40]=0;},b=>{b[14]^=1;},b=>{b[0]=0;}]){const wrong=Buffer.from(zip);mutate(wrong);assert.throws(()=>decodeBootstrapZIP(wrong,expected));}
  assert.throws(()=>decodeBootstrapZIP(zip,{...expected,sha256:'0'.repeat(64)}));assert.throws(()=>decodeBootstrapZIP(zip,{...expected,bytes:body.length+1}));
});
test('Static bootstrap auditor rejects another machine and dynamic/interpreter program records',()=>{
  const elf=Buffer.alloc(120);elf.set(Buffer.from('7f454c460201','hex'));elf.writeUInt16LE(2,16);elf.writeUInt16LE(183,18);elf.writeBigUInt64LE(0x1000n,24);elf.writeBigUInt64LE(64n,32);elf.writeUInt16LE(56,54);elf.writeUInt16LE(1,56);elf.writeUInt32LE(1,64);elf.writeUInt32LE(1,68);elf.writeBigUInt64LE(0x1000n,80);elf.writeBigUInt64LE(1n,104);
  assert.doesNotThrow(()=>assertStaticBootstrap(elf,'arm64'));assert.throws(()=>assertStaticBootstrap(elf,'amd64'));
  for(const type of [2,3]){const wrong=Buffer.from(elf);wrong.writeUInt32LE(type,64);assert.throws(()=>assertStaticBootstrap(wrong,'arm64'));}
});
function manifest(){return{format:'nextgo-lambda-v1',runtime:'provided.al2023',handler:'bootstrap',goos:'linux',goarch:'arm64',cgoEnabled:'0',architecture:'arm64',isaBaseline:'GOARM64=v8.0',elf:{machine:'EM_AARCH64',class:'ELFCLASS64',type:'ET_EXEC',static:true,goVersion:'go1.27.3'},runtimeId:'a'.repeat(20),sourceFingerprint:'b'.repeat(64),assets:[{embeddedPath:'public/retained.css',defaultRequestPath:'/assets/retained.css',bytes:10,sha256:'c'.repeat(64)}]};}
test('Actual deployment descriptor cannot borrow another architecture ISA runtime identity or unsafe asset mapping',()=>{
  assert.doesNotThrow(()=>validateLambdaManifest(manifest(),{arch:'arm64',goVersion:'1.27.3'}));
  for(const mutate of [m=>{m.architecture='x86_64';},m=>{m.isaBaseline='GOARM64=v8.1';},m=>{m.runtimeId='bad';},m=>{m.sourceFingerprint='bad';},m=>{m.assets[0].embeddedPath='../outside';},m=>{m.assets.push({...m.assets[0]});},m=>{m.assets[0].defaultRequestPath='/assets/x?query';}]){const value=manifest();mutate(value);assert.throws(()=>validateLambdaManifest(value,{arch:'arm64',goVersion:'1.27.3'}));}
});
test('Static extracted bootstrap build-info cannot borrow nativeCGO1 foreign path or another ISA',()=>{
  const record='/owned/bootstrap: go1.27.3\n\tbuild\tGOOS=linux\n\tbuild\tGOARCH=arm64\n\tbuild\tCGO_ENABLED=0\n\tbuild\tGOARM64=v8.0\n';const options={arch:'arm64',goVersion:'1.27.3'};assert.doesNotThrow(()=>assertLambdaBuildInfo(record,'/owned/bootstrap',options));
  for(const invalid of ['/foreign'+record,record.replace('CGO_ENABLED=0','CGO_ENABLED=1'),record.replace('GOARM64=v8.0','GOARM64=v8.1'),record+record,record+'\tbuild\tGOARCH=amd64\n'])assert.throws(()=>assertLambdaBuildInfo(invalid,'/owned/bootstrap',options));
});
