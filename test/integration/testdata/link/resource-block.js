import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { evaluate, outcome, until } from './persistent.js';

export const resourceKinds = ['document', 'fetch', 'xhr', 'image', 'script', 'stylesheet', 'font', 'media', 'track', 'eventsource', 'beacon', 'websocket', 'preflight', 'manifest', 'prefetch'];

function action(kind, url, key) {
  const u=JSON.stringify(url), k=JSON.stringify(key);
  switch(kind) {
    case 'document': return `new Promise(resolve=>{const f=document.createElement('iframe');f.src=${u};f.onload=f.onerror=()=>resolve({attempted:true});document.body.append(f);setTimeout(()=>resolve({attempted:true}),4000);})`;
    case 'fetch': return `fetch(${u},{mode:'cors'}).then(r=>r.arrayBuffer()).then(()=>({attempted:true}),()=>({attempted:true}))`;
    case 'preflight': return `fetch(${u},{headers:{'X-Guard-Probe':${k}}}).then(()=>({attempted:true}),()=>({attempted:true}))`;
    case 'xhr': return `new Promise(resolve=>{const x=new XMLHttpRequest();x.open('GET',${u});x.timeout=4000;x.onload=x.onerror=x.ontimeout=()=>resolve({attempted:true});x.send();})`;
    case 'beacon': return `({attempted:navigator.sendBeacon(${u},${k})})`;
    case 'font': return `new FontFace('probe'+${k},'url('+${u}+')').load().then(()=>({attempted:true}),()=>({attempted:true}))`;
    case 'websocket': return `new Promise(resolve=>{const s=new WebSocket(${JSON.stringify(url.replace(/^http/,'ws'))});s.onopen=()=>{s.close();resolve({attempted:true})};s.onerror=()=>resolve({attempted:true});setTimeout(()=>{s.close();resolve({attempted:true})},4000);})`;
    case 'eventsource': return `new Promise(resolve=>{const s=new EventSource(${u});s.onmessage=s.onerror=()=>{s.close();resolve({attempted:true})};setTimeout(()=>{s.close();resolve({attempted:true})},4000);})`;
    case 'media': return `new Promise(resolve=>{const a=document.createElement('audio');a.preload='auto';a.src=${u};a.onloadedmetadata=a.onerror=()=>{a.remove();resolve({attempted:true})};document.body.append(a);a.load();setTimeout(()=>{a.remove();resolve({attempted:true})},4000);})`;
    case 'track': return `new Promise(resolve=>{const v=document.createElement('video');v.crossOrigin='anonymous';const t=document.createElement('track');t.src=${u};t.kind='captions';t.default=true;t.onload=t.onerror=()=>{v.remove();resolve({attempted:true})};v.append(t);document.body.append(v);t.track.mode='hidden';setTimeout(()=>{v.remove();resolve({attempted:true})},4000);})`;
    case 'manifest': return `(()=>{const e=document.createElement('link');e.rel='manifest';e.href=${u};document.head.append(e);return{attempted:true};})()`;
    case 'prefetch': return `(()=>{const e=document.createElement('link');e.rel='prefetch';e.href=${u};document.head.append(e);return{attempted:true};})()`;
    default: {
      const tag=kind==='image'?'img':kind==='script'?'script':'link';
      const set=kind==='stylesheet'?"e.rel='stylesheet';e.href=u;":'e.src=u;';
      return `new Promise(resolve=>{const u=${u},e=document.createElement('${tag}');${set}e.onload=e.onerror=()=>resolve({attempted:true});document.head.append(e);setTimeout(()=>resolve({attempted:true}),4000);})`;
    }
  }
}

export async function resourceBlockRow(getLink,opts) {
  const reader=await SocketClient.open(opts.ws);reader.callTimeout=15000;
  const client=opts.leg==='guarded'?await CdpClient.open(getLink(),opts.browser):reader;
  client.callTimeout=15000;
  let targetId;
  try {
    ({targetId}=await client.send('Target.createTarget',{url:opts.ordinary+'/probe',background:true}));
    const {sessionId}=await client.send('Target.attachToTarget',{targetId,flatten:true});
    const observer=client===reader?sessionId:(await reader.send('Target.attachToTarget',{targetId,flatten:true})).sessionId;
    await reader.send('Page.enable',{},observer);await reader.send('Network.enable',{},observer);
    await client.send('Page.enable',{},sessionId);
    if(opts.intercept) {
      client.on((e)=>{if(e.method==='Fetch.requestPaused'&&e.sessionId===sessionId&&!e.params.request.url.startsWith(opts.protected))client.send('Fetch.continueRequest',{requestId:e.params.requestId},sessionId).catch(()=>{});});
      await client.send('Fetch.enable',{patterns:[{urlPattern:'*',requestStage:'Request'}]},sessionId);
    }
    await until(async()=>await evaluate(reader,observer,'location.href')===opts.ordinary+'/probe'&&(await evaluate(reader,observer,'document.readyState'))==='complete','resource probe document');
    await reader.send('Page.bringToFront',{},observer);
    await evaluate(reader,observer,`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`);
    const path=`/resource?kind=${opts.kind}&key=${opts.key}`;
    const url=opts.redirect?`${opts.ordinary}/redirect?kind=${opts.kind}&key=${opts.key}&protected=${encodeURIComponent(opts.protected)}`:opts.protected+path;
    const start=reader.events.length;
    const trigger=await outcome(client.send('Runtime.evaluate',{expression:action(opts.kind,url,opts.key),awaitPromise:true,returnByValue:true},sessionId));
    if(!trigger.ok||trigger.result.exceptionDetails||!trigger.result.result.value?.attempted)throw new Error(`resource action did not run: ${JSON.stringify(trigger)}`);
    if(opts.kind==='manifest')await outcome(reader.send('Page.getAppManifest',{},observer));
    await until(()=>reader.events.slice(start).some((e)=>e.sessionId===observer&&((e.method==='Network.requestWillBeSent'&&e.params.request.url.includes(opts.key))||(e.method==='Network.webSocketCreated'&&e.params.url.includes(opts.key)))),'independent resource request observation',5000);
    const terminal=()=>{
      const relevant=reader.events.slice(start).filter((e)=>e.sessionId===observer);
      const ids=new Set(relevant.filter((e)=>(e.method==='Network.requestWillBeSent'&&e.params.request.url.includes(opts.key))||(e.method==='Network.webSocketCreated'&&e.params.url.includes(opts.key))).map((e)=>e.params.requestId));
      const frames=new Set(relevant.filter((e)=>e.method==='Network.requestWillBeSent'&&e.params.request.url.includes(opts.key)).map((e)=>e.params.frameId));
      return relevant.filter((e)=>(ids.has(e.params.requestId)&&['Network.loadingFinished','Network.loadingFailed','Network.webSocketHandshakeResponseReceived','Network.webSocketFrameError'].includes(e.method))||(opts.kind==='document'&&e.method==='Page.frameStoppedLoading'&&frames.has(e.params.frameId)));
    };
    await until(()=>terminal().length>0,'resource terminal network outcome',5000);
    const events=reader.events.slice(start).filter((e)=>e.sessionId===observer&&e.method.startsWith('Network.'));
    const pauses=client.events.filter((e)=>e.method==='Fetch.requestPaused'&&e.params.request.url.startsWith(opts.protected));
    await outcome(reader.send('Target.closeTarget',{targetId}));targetId=undefined;
    const counts=await(await fetch(opts.counts+'?key='+encodeURIComponent(opts.key))).json();
    return {kind:opts.kind,key:opts.key,leg:opts.leg,redirect:opts.redirect,intercept:opts.intercept,trigger,counts,events,terminal:terminal(),protectedPausesShown:pauses.length};
  } finally {
    if(targetId)await outcome(reader.send('Target.closeTarget',{targetId}));
    if(client!==reader)await client.close();reader.close();
  }
}
