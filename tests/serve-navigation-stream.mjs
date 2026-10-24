import {createServer,request} from 'node:http';

const used=new Set();
const server=createServer((req,res)=>{
 const url=new URL(req.url,'http://127.0.0.1:4140');
 const fault=url.searchParams.get('fault');const key=url.searchParams.get('key');
 const modify=req.headers['x-nextgo-stream']==='1'&&fault&&key&&!used.has(key);
 if(modify)used.add(key);
 const upstream=request({hostname:'127.0.0.1',port:4100,path:req.url,method:req.method,headers:{...req.headers,host:'127.0.0.1:4140','accept-encoding':'identity'}},response=>{
  const headers={...response.headers};delete headers['content-length'];
  if(modify&&fault==='missing-header')delete headers['x-nextgo-stream'];
  res.writeHead(response.statusCode,headers);
  if(!modify||!String(headers['content-type']).includes('application/x-nextgo-stream+json')){response.pipe(res);return}
  let pending='';let ended=false;let chunks=0;
  response.setEncoding('utf8');response.on('data',data=>{if(ended)return;pending+=data;let end;
   while((end=pending.indexOf('\n'))>=0){let line=pending.slice(0,end);pending=pending.slice(end+1);let frame=JSON.parse(line);
    if(frame.type==='shell'){
     if(fault==='build-skew'){frame.page.build='incompatible-stream';line=JSON.stringify(frame)}
     res.write(line+'\n');
     if(fault==='truncate'){ended=true;res.end();response.destroy();return}
     if(fault==='unknown'){res.end(JSON.stringify({version:1,type:'chunk',id:'unregistered',html:'<p data-malicious-stream>must not appear</p>'})+'\n');ended=true;response.destroy();return}
    }else if(frame.type==='chunk'){
     chunks++;
     if(fault==='ownership'){frame.html='<nextgo-layout data-nextgo-layout="forged">must not appear</nextgo-layout>';line=JSON.stringify(frame)}
     res.write(line+'\n');
     if(fault==='duplicate'&&chunks===1)res.write(line+'\n');
    }else{res.write(line+'\n');if(fault==='after-done')res.write(JSON.stringify({version:1,type:'chunk',id:'unregistered',html:'<p data-malicious-stream>must not appear</p>'})+'\n')}
   }
  });response.on('end',()=>{if(!ended){ended=true;res.end(pending)}});response.on('error',()=>res.destroy());
 });
 upstream.on('error',()=>{if(!res.headersSent)res.writeHead(502);res.end('upstream unavailable')});req.pipe(upstream);res.on('close',()=>upstream.destroy());
});
server.listen(4140,'127.0.0.1');for(const signal of ['SIGTERM','SIGINT'])process.on(signal,()=>server.close(()=>process.exit(0)));
