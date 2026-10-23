import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { auditSetter, evaluate, outcome, until } from './persistent.js';

export async function clientResourceRow(getLink,opts) {
  const reader=await SocketClient.open(opts.ws);reader.callTimeout=15000;
  const client=opts.leg==='guarded'?await CdpClient.open(getLink(),opts.browser):reader;client.callTimeout=15000;
  let targetId;
  try {
    ({targetId}=await client.send('Target.createTarget',{url:opts.ordinary+'/probe',background:true}));
    const {sessionId}=await client.send('Target.attachToTarget',{targetId,flatten:true});
    const observer=client===reader?sessionId:(await reader.send('Target.attachToTarget',{targetId,flatten:true})).sessionId;
    await reader.send('Page.enable',{},observer);await reader.send('Network.enable',{},observer);
    await client.send('Page.enable',{},sessionId);await client.send('Network.enable',{},sessionId);
    await until(async()=>await evaluate(reader,observer,'location.href')===opts.ordinary+'/probe'&&(await evaluate(reader,observer,'document.readyState'))==='complete','client resource ordinary document');
    let params, prime;
    if(opts.method==='Network.replayXHR') {
      const url=`${opts.ordinary}/replay?kind=xhr&key=${opts.key}`;
      const start=reader.events.length;
      const source=`new Promise(resolve=>{const x=globalThis.__clientReplay=new XMLHttpRequest();x.open('GET',${JSON.stringify(url)});x.onload=()=>resolve({ok:true,status:x.status});x.onerror=()=>resolve({ok:false});x.send();})`;
      const loaded=await evaluate(reader,observer,source);
      if(!loaded.ok||loaded.status!==200)throw new Error('ordinary replay prime did not succeed');
      const event=await until(()=>reader.events.slice(start).find((e)=>e.sessionId===observer&&e.method==='Network.requestWillBeSent'&&e.params.type==='XHR'&&e.params.request.url===url),'ordinary XHR identifier');
      await until(()=>client.events.some((e)=>e.sessionId===sessionId&&e.method==='Network.responseReceived'&&e.params.requestId===event.params.requestId),'client observed ordinary XHR response');
      prime={url,requestId:event.params.requestId};params={requestId:event.params.requestId};
      const armed=await fetch(opts.arm+'?key='+encodeURIComponent(opts.key)+'&protected='+encodeURIComponent(opts.protected));
      if(!armed.ok)throw new Error('replay fixture did not change to protected redirect');
    } else {
      const url=opts.redirect?`${opts.ordinary}/redirect?kind=fetch&key=${opts.key}&protected=${encodeURIComponent(opts.protected)}`:`${opts.protected}/resource?kind=fetch&key=${opts.key}`;
      params={frameId:targetId,url,options:{disableCache:true,includeCredentials:false}};
    }
    const before=client.sent.length;
    const networkStart=reader.events.length;
    const setup=await outcome(client.send(opts.method,params,sessionId));
    if(!setup.ok&&!Number.isInteger(setup.error?.code))throw new Error('client fetch has no structured terminal reply');
    const audit=client===reader?{applicable:false,transport:'direct-observer'}:await auditSetter(opts.gate,sessionId,client.sent[before].id,setup);
    if(setup.ok&&opts.method==='Network.replayXHR') {
      await until(async()=>{const c=await(await fetch(opts.counts+'?key='+encodeURIComponent(opts.key))).json();return c.ordinary>=2&&c;},'native replay request observed',5000);
      const replay=await until(()=>reader.events.slice(networkStart).find((e)=>e.sessionId===observer&&e.method==='Network.requestWillBeSent'&&e.params.request.url===prime.url),'native replay network request',5000);
      await until(()=>reader.events.slice(networkStart).some((e)=>e.sessionId===observer&&['Network.loadingFinished','Network.loadingFailed'].includes(e.method)&&e.params.requestId===replay.params.requestId),'native replay terminal',5000);
    }
    if(setup.ok&&opts.method==='Network.loadNetworkResource'&&setup.result.resource?.stream)await outcome(client.send('IO.close',{handle:setup.result.resource.stream},sessionId));
    await outcome(reader.send('Target.closeTarget',{targetId}));targetId=undefined;
    const counts=await(await fetch(opts.counts+'?key='+encodeURIComponent(opts.key))).json();
    return {method:opts.method,key:opts.key,leg:opts.leg,redirect:opts.redirect,protected:opts.protected,setup,audit,params,prime,counts};
  } finally {
    if(targetId)await outcome(reader.send('Target.closeTarget',{targetId}));if(client!==reader)await client.close();reader.close();
  }
}
