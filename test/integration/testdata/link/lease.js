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
 let key,jwk,jkt,request,data,jwt,prior,sequence;
 const open=async args=>{const s=await link().open({kind:'cdp-lease-v1',...args,jwk});const bind=args.mode==='data'?await digest(text.encode(args.jwt)):un64(jkt);
 const msg=new Uint8Array([...text.encode('slicc-cdp-lease-v1:'+args.mode),0,...un64(s.opened.nonce),...bind]);const sig=await crypto.subtle.sign('Ed25519',key.privateKey,msg);await s.write(record({type:'proof',sig:b64(new Uint8Array(sig))}));return s;};
 return {
 async init(){key=await crypto.subtle.generateKey('Ed25519',false,['sign','verify']);const {x}=await crypto.subtle.exportKey('jwk',key.publicKey);jwk={crv:'Ed25519',kty:'OKP',x};jkt=b64(await digest(text.encode(JSON.stringify(jwk))));return jkt;},
 async request(allow,extra){request=await open({mode:'request',allow,...extra});return read(request);},
 async grant(){const r=await read(request);request.end();if(r.type==='grant'){prior=jwt;jwt=r.jwt;return JSON.parse(dec.decode(un64(jwt.split('.')[1]))).jti;}return r.reason;},
 async connect(old=false){try{data=await open({mode:'data',jwt:old?prior:jwt});sequence=0;return (await read(data)).type;}catch(e){return e.code;}},
 async action(op,args){await data.write(record({type:'action',id:++sequence,op,args}));return read(data);},
 async closed(){await data.done;return true;},
 };
}
