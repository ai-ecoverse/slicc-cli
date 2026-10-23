import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { auditSetter, evaluate, outcome, until } from './persistent.js';

export async function clientMutationRow(getLink,opts) {
  const reader=await SocketClient.open(opts.ws);reader.callTimeout=15000;
  const client=opts.leg==='guarded'?await CdpClient.open(getLink(),opts.browser):reader;client.callTimeout=15000;
  const legacy=opts.method==='Network.continueInterceptedRequest';
  const auth=opts.method==='Fetch.continueWithAuth';
  let targetId,owned;
  try {
    ({targetId}=await client.send('Target.createTarget',{url:opts.ordinary+'/probe',background:true}));
    const {sessionId}=await client.send('Target.attachToTarget',{targetId,flatten:true});
    const observer=client===reader?sessionId:(await reader.send('Target.attachToTarget',{targetId,flatten:true})).sessionId;
    await reader.send('Page.enable',{},observer);await reader.send('Network.enable',{},observer);
    await client.send('Page.enable',{},sessionId);await client.send('Network.enable',{},sessionId);
    await until(async()=>await evaluate(reader,observer,'location.href')===opts.ordinary+'/probe'&&(await evaluate(reader,observer,'document.readyState'))==='complete','mutation ordinary document');
    let selected;
    const paused=legacy?'Network.requestIntercepted':'Fetch.requestPaused';
    const proceed=(e)=>legacy?client.send('Network.continueInterceptedRequest',{interceptionId:e.params.interceptionId},sessionId):client.send('Fetch.continueRequest',{requestId:e.params.requestId},sessionId);
    client.on((e)=>{
      if(e.sessionId!==sessionId)return;
      if(e.method===paused) {
        if(!selected&&e.params.request.url.includes('/'+(auth?'challenge':'initial')+'?')) { selected=e;owned=e; if(auth)proceed(e).catch(()=>{}); }
        else proceed(e).catch(()=>{});
      }
      if(auth&&e.method==='Fetch.authRequired'&&e.params.request.url.includes(opts.key)) { selected=e;owned=e; }
    });
    if(legacy)await client.send('Network.setRequestInterception',{patterns:[{urlPattern:'*',interceptionStage:'Request'}]},sessionId);
    else await client.send('Fetch.enable',{patterns:[{urlPattern:'*',requestStage:'Request'}],handleAuthRequests:auth},sessionId);
    const to=opts.redirect?`${opts.ordinary}/redirect?kind=fetch&key=${opts.key}&protected=${encodeURIComponent(opts.protected)}`:`${opts.protected}/resource?kind=fetch&key=${opts.key}`;
    const start=reader.events.length;
    const original=`${opts.ordinary}/${auth?'challenge':'initial'}?key=${opts.key}&to=${encodeURIComponent(to)}`;
    await reader.send('Runtime.evaluate',{expression:`globalThis.__mutationResult=undefined;globalThis.__mutationFetch=fetch(${JSON.stringify(original)}).then(r=>r.text()).then(()=>globalThis.__mutationResult='finished',()=>globalThis.__mutationResult='failed');`},observer);
    await until(()=>selected&&(!auth||selected.method==='Fetch.authRequired'),'client-owned paused request',5000);
    let params;
    if(legacy)params={interceptionId:selected.params.interceptionId,url:to};
    else if(auth)params={requestId:selected.params.requestId,authChallengeResponse:{response:'ProvideCredentials',username:'fixture',password:'fixture'}};
    else if(opts.method==='Fetch.fulfillRequest')params={requestId:selected.params.requestId,responseCode:302,responseHeaders:[{name:'Location',value:to},{name:'Access-Control-Allow-Origin',value:'*'}],body:''};
    else params={requestId:selected.params.requestId,url:to};
    const before=client.sent.length;
    const setup=await outcome(client.send(opts.method,params,sessionId));
    if(!setup.ok&&!Number.isInteger(setup.error?.code))throw new Error('mutation has no structured terminal reply');
    const audit=client===reader?{applicable:false,transport:'direct-observer'}:await auditSetter(opts.gate,sessionId,client.sent[before].id,setup);
    if(setup.ok) {
      await until(async()=>await evaluate(reader,observer,'globalThis.__mutationResult'),'page fetch terminal',5000);
      const ids=new Set(reader.events.slice(start).filter((e)=>e.sessionId===observer&&e.method==='Network.requestWillBeSent'&&e.params.request.url.includes(opts.key)).map((e)=>e.params.requestId));
      await until(()=>reader.events.slice(start).some((e)=>e.sessionId===observer&&ids.has(e.params.requestId)&&['Network.loadingFinished','Network.loadingFailed'].includes(e.method)),'matching mutated request terminal',5000);
    }
    await outcome(reader.send('Target.closeTarget',{targetId}));targetId=undefined;
    const counts=await(await fetch(opts.counts+'?key='+encodeURIComponent(opts.key))).json();
    return {method:opts.method,key:opts.key,leg:opts.leg,redirect:opts.redirect,protected:opts.protected,setup,audit,params,original,pause:selected,counts,events:reader.events.slice(start).filter((e)=>e.sessionId===observer&&e.method.startsWith('Network.'))};
  } finally {
    if(targetId)await outcome(reader.send('Target.closeTarget',{targetId}));if(client!==reader)await client.close();reader.close();
  }
}
