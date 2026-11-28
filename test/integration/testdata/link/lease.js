const text = new TextEncoder();
const dec = new TextDecoder();
const b64 = bytes => Uint8Array.from(bytes).toBase64({alphabet:'base64url',omitPadding:true});
const un64 = value => Uint8Array.fromBase64(value,{alphabet:'base64url'});
const digest = async bytes => new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
function record(obj) {
  const b=text.encode(JSON.stringify(obj)), out=new Uint8Array(4+b.length);
  new DataView(out.buffer).setUint32(0,b.length);out.set(b,4);return out;
}
async function read(s) {
  const b=await s.read();if(!b||new DataView(b.buffer,b.byteOffset,b.byteLength).getUint32(0)!==b.length-4)throw Error('bad fixture record');
  return JSON.parse(dec.decode(b.subarray(4)));
}
export function leaseFixture(link) {
 let key,jwk,jkt,request,data,jwt,prior,sequence,frames=[];
 const tap=s=>{const on=s.onFrame.bind(s);s.onFrame=f=>{const body=f.payload??new Uint8Array(0);frames.push({t:performance.now(),w:Date.now(),type:f.type,text:f.type===3&&body.byteLength>4?dec.decode(body.subarray(4)):f.type===5?dec.decode(body):''});return on(f);};return s;};
 const open=async args=>{const s=await link().open({kind:'cdp-lease-v1',...args,jwk});const bind=args.mode==='data'?await digest(text.encode(args.jwt)):un64(jkt);
 const msg=new Uint8Array([...text.encode('slicc-cdp-lease-v1:'+args.mode),0,...un64(s.opened.nonce),...bind]);const sig=await crypto.subtle.sign('Ed25519',key.privateKey,msg);await s.write(record({type:'proof',sig:b64(new Uint8Array(sig))}));return s;};
 return {
 async init(){key=await crypto.subtle.generateKey('Ed25519',false,['sign','verify']);const {x}=await crypto.subtle.exportKey('jwk',key.publicKey);jwk={crv:'Ed25519',kty:'OKP',x};jkt=b64(await digest(text.encode(JSON.stringify(jwk))));return jkt;},
 async request(allow,extra){request=await open({mode:'request',allow,...extra});return read(request);},
 async grant(){const r=await read(request);request.end();if(r.type==='grant'){prior=jwt;jwt=r.jwt;return JSON.parse(dec.decode(un64(jwt.split('.')[1]))).jti;}return r.reason;},
 async connect(old=false){try{frames=[];data=tap(await open({mode:'data',jwt:old?prior:jwt}));sequence=0;return (await read(data)).type;}catch(e){return e.code;}},
 async action(op,args){await data.write(record({type:'action',id:++sequence,op,args}));return read(data);},
 async closed(){await data.done;return true;},
 now(){return performance.now();},
 seen(){return frames;},
 claims(){return JSON.parse(dec.decode(un64(jwt.split('.')[1])));},
 async tryRequest(allow,extra){try{return await this.request(allow,extra);}catch(e){return {type:'refused',code:e.code??String(e.message)};}},
 async tryGrant(){try{return await this.grant();}catch(e){return e.code??String(e.message);}},
 async dispatch(op,args){const id=++sequence;await data.write(record({type:'action',id,op,args}));return id;},
 async collect(){try{return await read(data);}catch(e){return {type:'end',code:e.code??String(e.message)};}},
 endData(){data.end();return true;},
 resetData(){data.reset('ECANCELED');return true;},
 adopt(other){jwt=other;return true;},
 token(){return jwt;},
 };
}
