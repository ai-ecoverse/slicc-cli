import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { auditSetter, evaluate, outcome, until } from './persistent.js';

export const stickyMethods = ['Network.setExtraHTTPHeaders', 'Network.setUserAgentOverride', 'Emulation.setUserAgentOverride', 'Emulation.setLocaleOverride', 'Emulation.setTimezoneOverride', 'Network.setCacheDisabled', 'Network.emulateNetworkConditions', 'Network.emulateNetworkConditionsByRule', 'Network.overrideNetworkState', 'Network.setBlockedURLs'];

export function stickyConfiguration(method, marker) {
  switch (method) {
    case 'Network.setExtraHTTPHeaders': return { headers: { 'X-Slicc-503': marker } };
    case 'Network.setUserAgentOverride':
    case 'Emulation.setUserAgentOverride': return { userAgent: `slicc503-${marker}` };
    case 'Emulation.setLocaleOverride': return { locale: 'de-DE' };
    case 'Emulation.setTimezoneOverride': return { timezoneId: 'Pacific/Kiritimati' };
    case 'Network.setCacheDisabled': return { cacheDisabled: true };
    case 'Network.emulateNetworkConditions': return { offline: false, latency: 1000, downloadThroughput: -1, uploadThroughput: -1 };
    case 'Network.emulateNetworkConditionsByRule': return { offline: false, matchedNetworkConditions: [{ urlPattern: '', latency: 1000, downloadThroughput: -1, uploadThroughput: -1 }] };
    case 'Network.overrideNetworkState': return { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 };
    case 'Network.setBlockedURLs': return { urls: ['*503-data*'] };
    default: throw new Error(`unknown sticky capability ${method}`);
  }
}

async function stickyProbe(reader, session, method, marker, url, contextId) {
  const identity = await evaluate(reader, session, `({ href: location.href, origin: self.origin, restored: globalThis.__stickyRestored === true })`, contextId);
  let expression;
  switch (method) {
    case 'Emulation.setLocaleOverride': expression = `({eligible:true,effect:new Intl.NumberFormat().resolvedOptions().locale==='de-DE',value:new Intl.NumberFormat().resolvedOptions().locale})`; break;
    case 'Emulation.setTimezoneOverride': expression = `({eligible:true,effect:new Intl.DateTimeFormat().resolvedOptions().timeZone==='Pacific/Kiritimati',value:new Intl.DateTimeFormat().resolvedOptions().timeZone})`; break;
    case 'Network.overrideNetworkState': expression = `({eligible:true,effect:navigator.onLine===false,value:navigator.onLine})`; break;
    default: expression = `(async () => {
      const method = ${JSON.stringify(method)};
      const options = method === 'Network.setCacheDisabled' ? {} : {cache:'no-store'};
      const before = performance.now();
      let data, second, error;
      try { data = await (await fetch(${JSON.stringify(url)},options)).json(); second = await (await fetch(${JSON.stringify(url)},options)).json(); }
      catch(e) { error=String(e); }
      const ms=performance.now()-before;
      if (method.includes('UserAgentOverride')) return {eligible:true,effect:navigator.userAgent===${JSON.stringify(`slicc503-${marker}`)},value:navigator.userAgent,http:data,ms,error};
      if (method==='Network.setBlockedURLs') return {eligible:true,effect:null,error,ms};
      const eligible=Boolean(data&&second);
      const effect=!eligible?null:method==='Network.setExtraHTTPHeaders'?data.header===${JSON.stringify(marker)}:method==='Network.setCacheDisabled'?data.count===1&&second.count===2:ms>=1800;
      return {eligible,effect,data,second,ms,error};
    })()`;
  }
  const start = reader.events.length;
  const observed = await evaluate(reader, session, expression, contextId);
  if (method === 'Network.setBlockedURLs') {
    const request = reader.events.slice(start).find((e) => e.sessionId === session && e.method === 'Network.requestWillBeSent' && e.params.request.url === url);
    const failed = request && reader.events.slice(start).find((e) => e.sessionId === session && e.method === 'Network.loadingFailed' && e.params.requestId === request.params.requestId);
    observed.blockedReason = failed?.params.blockedReason ?? null;
    observed.effect = failed?.params.blockedReason === 'inspector';
    observed.eligible = Boolean(request) && (!observed.error || Boolean(failed));
    if (!observed.eligible) observed.effect = null;
  }
  if (observed.eligible && typeof observed.effect !== 'boolean') throw new Error('sticky observer effect is not a measured boolean');
  return {...identity,...observed};
}

export async function measureSticky(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 20000;
  const guarded = opts.leg === 'guarded';
  let client, context, targetId;
  try {
    ({browserContextId:context}=await reader.send('Target.createBrowserContext',{}));
    const ordinary=`${opts.ordinary}/doc?marker=${opts.marker}&stage=ordinary`;
    const protectedURL=`${opts.protected}/doc?marker=${opts.marker}&stage=protected`;
    ({targetId}=await reader.send('Target.createTarget',{url:ordinary,browserContextId:context,background:true}));
    const {sessionId:observer}=await reader.send('Target.attachToTarget',{targetId,flatten:true});
    await reader.send('Page.enable',{},observer);
    await reader.send('Runtime.enable',{},observer);
    await reader.send('Network.enable',{},observer);
    await until(async()=>await evaluate(reader,observer,'document.readyState')==='complete','initial sticky ordinary document');
    const navigate=async(url)=>{
      const start=reader.events.length;
      const reply=await outcome(reader.send('Page.navigate',{url},observer));
      const e=await until(()=>reader.events.slice(start).find((e)=>e.sessionId===observer&&e.method==='Page.frameNavigated'&&e.params.frame.id===targetId),'sticky frame commit');
      const frame=e.params.frame;
      const blocked=frame.unreachableUrl===url&&reply.result?.errorText==='net::ERR_BLOCKED_BY_CLIENT';
      if (!frame.unreachableUrl) await until(async()=>await evaluate(reader,observer,'location.href')===url&&(await evaluate(reader,observer,'document.readyState'))==='complete','sticky committed document');
      return {reply,frame,type:e.params.type,blocked};
    };
    let seed, historyEntry, protectedParent;
    if (opts.mode==='history'||opts.mode==='bfcache') {
      seed=await navigate(protectedURL);
      if(seed.blocked||seed.frame.url!==protectedURL) throw new Error('raw protected history seed did not commit');
      await evaluate(reader,observer,opts.mode==='bfcache'?`addEventListener('pageshow',(e)=>{globalThis.__stickyRestored=e.persisted;});true`:`addEventListener('unload',()=>{});true`);
      const history=await reader.send('Page.getNavigationHistory',{},observer);
      historyEntry=history.entries[history.currentIndex];
      if(historyEntry.url!==protectedURL)throw new Error('protected history entry not observed');
      await navigate(`${ordinary}&after-seed=1`);
    }
    if(opts.mode.startsWith('inherited-')) {
      protectedParent=`${opts.protected}/doc?marker=${opts.marker}&stage=parent`;
      await evaluate(reader,observer,`(() => {const f=document.createElement('iframe');f.name='stickyParent';f.src=${JSON.stringify(protectedParent)};document.body.append(f);return true;})()`);
      await until(async()=>{
        const tree=await reader.send('Page.getFrameTree',{},observer);
        return tree.frameTree.childFrames?.some((f)=>f.frame.url===protectedParent);
      },'protected parent committed BEFORE guard attachment');
      const parent=await until(()=>reader.events.findLast((e)=>e.sessionId===observer&&e.method==='Runtime.executionContextCreated'&&e.params.context.auxData?.isDefault&&e.params.context.origin===opts.protected)?.params.context,'pre-existing protected parent context');
      await until(async()=>await evaluate(reader,observer,'document.readyState',parent.id)==='complete','pre-existing protected parent loaded');
    }
    client=guarded?await CdpClient.open(getLink(),opts.browser):reader;
    client.callTimeout=20000;
    const session=guarded?(await client.send('Target.attachToTarget',{targetId,flatten:true})).sessionId:observer;
    await client.send('Page.enable',{},session);
    await client.send('Network.enable',{},session);
    const before=client.sent.length;
    const setup=opts.leg==='baseline'?null:await outcome(client.send(opts.method,stickyConfiguration(opts.method,opts.marker),session));
    if(setup&&!setup.ok&&!Number.isInteger(setup.error?.code))throw new Error('sticky setter lacks terminal CDP reply');
    const audit=guarded?await auditSetter(opts.gate,session,client.sent[before].id,setup):{applicable:false,transport:'direct-observer'};
    if(setup&&!setup.ok&&setup.error.code===-32601)return {method:opts.method,mode:opts.mode,leg:opts.leg,supported:false,setup,audit};
    const ordinaryProbe=await stickyProbe(reader,observer,opts.method,opts.marker,`${opts.ordinary}/503-data?key=${opts.marker}-ordinary`);
    let commit, contextId;
    if(opts.mode.startsWith('inherited-')) {
      const kind=opts.mode.endsWith('blank')?'blank':'srcdoc';
      const expected=kind==='blank'?'about:blank':'about:srcdoc';
      const parent=await until(()=>reader.events.findLast((e)=>e.sessionId===observer&&e.method==='Runtime.executionContextCreated'&&e.params.context.auxData?.isDefault&&e.params.context.origin===opts.protected)?.params.context,'protected parent live context');
      await evaluate(reader,observer,`(() => { const f=document.createElement('iframe');f.name='stickyChild';${kind==='srcdoc'?"f.srcdoc='<p>inherited child</p>';":''}document.body.append(f);return true;})()`,parent.id);
      const layout=await until(async()=>{
        const {frameTree}=await reader.send('Page.getFrameTree',{},observer);
        const p=frameTree.childFrames?.find((f)=>f.frame.url===protectedParent);
        const child=p?.childFrames?.find((f)=>f.frame.name==='stickyChild'&&f.frame.url===expected);
        return child&&{parent:p.frame,child:child.frame};
      },'new inherited protected child committed AFTER setter');
      const world=await until(async()=>{
        const c=reader.events.findLast((e)=>e.sessionId===observer&&e.method==='Runtime.executionContextCreated'&&e.params.context.auxData?.isDefault&&e.params.context.auxData.frameId===layout.child.id)?.params.context;
        return c&&(await evaluate(reader,observer,'location.href',c.id))===expected&&(await evaluate(reader,observer,'self.origin',c.id))===opts.protected&&c;
      },'protected inherited default world');
      contextId=world.id;
      commit={...layout,contextId,origin:opts.protected};
    } else if(opts.mode==='history'||opts.mode==='bfcache') {
      const start=reader.events.length;
      const restore=await outcome(reader.send('Page.navigateToHistoryEntry',{entryId:historyEntry.id},observer));
      const e=await until(()=>reader.events.slice(start).find((e)=>e.sessionId===observer&&e.method==='Page.frameNavigated'&&e.params.frame.id===targetId&&(e.params.frame.url===protectedURL||e.params.frame.unreachableUrl===protectedURL)),'protected history restore or explicit blocking');
      const blocked=e.params.frame.unreachableUrl===protectedURL;
      const bfcache=e.params.type==='BackForwardCacheRestore';
      const reasons=reader.events.slice(start).filter((e)=>e.sessionId===observer&&e.method==='Page.backForwardCacheNotUsed');
      if(!blocked)await until(async()=>await evaluate(reader,observer,'document.readyState')==='complete','restored protected document loaded');
      if(opts.mode==='bfcache'&&bfcache)await until(async()=>await evaluate(reader,observer,'globalThis.__stickyRestored===true'),'independent persisted pageshow');
      if(opts.mode==='bfcache'&&!blocked&&!bfcache&&reasons.length===0)throw new Error('BFCache restore missing without native non-restoration reasons');
      if(opts.mode==='history'&&bfcache)throw new Error('history row unexpectedly used BFCache');
      commit={seed,historyEntry,restore,frame:e.params.frame,type:e.params.type,blocked,bfcache,reasons};
    } else {
      commit=await navigate(opts.mode==='error'?`${opts.protected}/fail?marker=${opts.marker}`:protectedURL);
    }
    if(commit.blocked&&opts.mode!=='error')return {method:opts.method,mode:opts.mode,leg:opts.leg,supported:true,setup,audit,ordinaryProbe,boundaryBlocked:true,commit};
    if(opts.mode==='error'&&(commit.frame.url!=='chrome-error://chromewebdata/'||!commit.frame.unreachableUrl?.startsWith(opts.protected+'/fail?')))throw new Error('protected-intent error commit not independently verified');
    const protectedProbe=await stickyProbe(reader,observer,opts.method,opts.marker,`${opts.protected}/503-data?key=${opts.marker}-protected`,contextId);
    if(opts.mode!=='error'&&protectedProbe.origin!==opts.protected)throw new Error('sticky probe not in protected origin');
    return {method:opts.method,mode:opts.mode,leg:opts.leg,supported:true,setup,audit,ordinaryProbe,commit,protectedProbe};
  } finally {
    if(targetId)await outcome(reader.send('Target.closeTarget',{targetId}));
    if(client&&client!==reader)await client.close();
    if(context)await outcome(reader.send('Target.disposeBrowserContext',{browserContextId:context}));
    reader.close();
  }
}
