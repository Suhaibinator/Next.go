// Pure input/identity tests only: no child, /proc, browser, network or fake app.
import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeIdentityLimits,parseProcStat,parseProcStatus,validateNativeIdentity,validateStableNativeSnapshot} from './retained-routes-native-identity.mjs';

const processPID=456,ownerUID=1000,start='9007199254740993';
const exe={path:'/tmp/owned-retained/server',dev:'9007199254740995',inode:'9007199254740997'};
const expected={pid:processPID,uid:ownerUID,executable:exe};
const clone=value=>JSON.parse(JSON.stringify(value));
function statText({comm='server',pid=String(processPID),pgid=String(processPID),session=String(processPID),ticks=start,fields:count=50,state='S'}={}){
  const fields=Array(count).fill('0');fields[0]=state;fields[1]='1';fields[2]=pgid;fields[3]=session;fields[19]=ticks;
  return `${pid} (${comm}) ${fields.join(' ')}\n`;
}
const statusText=(uids='1000\t1000\t1000\t1000',pid=String(processPID))=>`Name:\tserver\nPid:\t${pid}\nPPid:\t1\nUid:\t${uids}\nGid:\t1000\t1000\t1000\t1000\n`;
const identity=()=>({platform:'linux',pid:processPID,uids:{real:ownerUID,effective:ownerUID,saved:ownerUID,filesystem:ownerUID},pgid:processPID,session:processPID,startTicks:start,executable:{...exe}});
function rejected(fn,label){assert.throws(fn,/Retained native identity:/,label);}

test('stat uses the final comm delimiter and preserves full decimal start precision',()=>{
  for(const comm of ['server','a ) b ( c','tricky ) S 1 2 ) ( child','line\nname','']){
    const value=parseProcStat(statText({comm}));
    assert.deepEqual(value,{pid:processPID,pgid:processPID,session:processPID,startTicks:start});
    assert.ok(Object.isFrozen(value));assert.equal(typeof value.startTicks,'string');
  }
  assert.equal(parseProcStat(statText({ticks:'18446744073709551615'})).startTicks,'18446744073709551615');
});

test('stat rejects malformed delimiters types counts and numeric bounds',()=>{
  for(const raw of [null,42,{},Buffer.from('not a string'),'x'.repeat(nativeIdentityLimits.statBytes+1),statText().replace(' (',' '),statText().replace(') ',')'),statText().replace(') S ',') SS '),statText().replace(') S ',') S  '),statText({pid:'0'}),statText({pid:'2147483648'}),statText({pid:'0456'}),statText({pgid:'0'}),statText({session:'-1'}),statText({ticks:'0'}),statText({ticks:'9007199254740993.0'}),statText({ticks:'1e5'}),statText({ticks:'01'}),statText({ticks:'18446744073709551616'}),statText({fields:49}),statText({fields:65}),statText().replace(' 1 ',' 18446744073709551616 '),statText().replace(' 1 ',' -9223372036854775809 '),statText().replace('server','bad\0comm')]){
    rejected(()=>parseProcStat(raw),typeof raw==='string'?raw.slice(0,80):'non-string');
  }
});

test('status parses all four explicit UIDs without rounding or inferring one from another',()=>{
  const parsed=parseProcStatus(statusText('0\t1000\t65534\t4294967295'));
  assert.deepEqual(parsed,{pid:processPID,uids:{real:0,effective:1000,saved:65534,filesystem:4294967295}});
  assert.ok(Object.isFrozen(parsed));assert.ok(Object.isFrozen(parsed.uids));
});

test('status rejects duplicate missing malformed oversized and noncanonical fields',()=>{
  for(const raw of [null,{},Buffer.from('Uid: 0 0 0 0'),statusText('1000 1000 1000'),statusText('1000 1000 1000 1000 1000'),statusText('-1 1000 1000 1000'),statusText('1.5 1000 1000 1000'),statusText('1000 1000 1000 4294967296'),statusText('01000 1000 1000 1000'),statusText('1000 1000 1000 1e3'),statusText('1000 1000 1000 1000','0'),statusText('1000 1000 1000 1000','2147483648'),statusText()+'Uid:\t1000\t1000\t1000\t1000\n',statusText()+'Pid:\t456\n',statusText().replace('Uid:','uid:'),statusText().replace('Pid:','pid:'),statusText().replace('server','bad\0name'),statusText().replace('server','bad\rname'),statusText().replace('server','\ud800'),'x'.repeat(nativeIdentityLimits.statusBytes+1),Array(nativeIdentityLimits.statusLines+1).fill('Name:\tx').join('\n'),'Name:\t'+'x'.repeat(nativeIdentityLimits.lineBytes)+'\n'+statusText()]){
    rejected(()=>parseProcStatus(raw),typeof raw==='string'?raw.slice(0,80):'non-string');
  }
});

test('identity records JSON-safe decimal executable identity and four owner UIDs',()=>{
  const actual=validateNativeIdentity(identity(),expected);
  assert.deepEqual(actual,identity());assert.deepEqual(JSON.parse(JSON.stringify(actual)),identity());
  assert.ok(Object.isFrozen(actual));assert.ok(Object.isFrozen(actual.uids));assert.ok(Object.isFrozen(actual.executable));
  assert.equal(typeof actual.executable.dev,'string');assert.equal(typeof actual.executable.inode,'string');
  assert.deepEqual(validateNativeIdentity(actual,expected,actual),actual);
});

test('identity rejects each PID PGID session UID start and executable mismatch',()=>{
  const mutations=[
    value=>{value.platform='darwin';},value=>{value.pid++;},value=>{value.pid=String(processPID);},value=>{value.pgid++;},value=>{value.session++;},
    ...['real','effective','saved','filesystem'].map(name=>value=>{value.uids[name]++;}),
    value=>{value.uids.saved='1000';},value=>{delete value.uids.filesystem;},value=>{value.startTicks=Number(start);},value=>{value.startTicks='0';},value=>{value.startTicks='01';},value=>{value.startTicks='18446744073709551616';},
    value=>{value.executable.path='/tmp/other/server';},value=>{value.executable.path='/tmp/owned-retained/../owned-retained/server';},value=>{value.executable.path='server';},value=>{value.executable.path+=' (deleted)';},value=>{value.executable.path='/'+ 'x'.repeat(nativeIdentityLimits.pathBytes);},
    value=>{value.executable.dev='9007199254740996';},value=>{value.executable.inode='9007199254740998';},value=>{value.executable.dev=1;},value=>{value.executable.inode='0';},value=>{value.executable.inode='01';},value=>{value.executable.inode='18446744073709551616';},
  ];
  for(const mutate of mutations){const actual=identity();mutate(actual);rejected(()=>validateNativeIdentity(actual,expected));}
  for(const contract of [{...expected,pid:0},{...expected,pid:2147483648},{...expected,uid:-1},{...expected,uid:'1000'},{...expected,uid:4294967296}])rejected(()=>validateNativeIdentity(identity(),contract));
  const newer=identity();newer.startTicks='9007199254740994';rejected(()=>validateNativeIdentity(newer,expected,identity()),'same numeric PID with a different lifetime');
});

test('stable bracket checks each status PID UID and process lifetime observation',()=>{
  const stat=parseProcStat(statText()),status=parseProcStatus(statusText());
  const snapshot={before:stat,firstStatus:status,executable:exe,lastStatus:status,after:stat};
  assert.deepEqual(validateStableNativeSnapshot(snapshot,expected),identity());
  const changes=[
    value=>{value.before.pid++;},value=>{value.after.pid++;},value=>{value.firstStatus.pid++;},value=>{value.lastStatus.pid++;},
    value=>{value.before.startTicks='9007199254740994';},value=>{value.after.startTicks='9007199254740994';},value=>{value.before.pgid++;},value=>{value.after.session++;},
    ...['real','effective','saved','filesystem'].flatMap(name=>['firstStatus','lastStatus'].map(which=>value=>{value[which].uids[name]++;})),
  ];
  for(const change of changes){const value=clone(snapshot);change(value);rejected(()=>validateStableNativeSnapshot(value,expected));}
});

test('parser and receipt ceilings are fixed and bounded',()=>{
  assert.deepEqual(nativeIdentityLimits,{statBytes:8192,statusBytes:16384,statusLines:128,lineBytes:1024,pathBytes:4096,statFields:64,receipts:8});
  assert.ok(Object.isFrozen(nativeIdentityLimits));
  assert.doesNotThrow(()=>parseProcStat(statText({fields:nativeIdentityLimits.statFields})));
  assert.doesNotThrow(()=>parseProcStatus(statusText()+Array(nativeIdentityLimits.statusLines-5).fill('Name:\tx').join('\n')));
});
