(() => {
  const stale = {status: 'stale'};
  const unsure = {status: 'unknown'};
  const placed = () => window === window.top && window.origin === p.origin && location.href === p.url && Date.now() < p.deadline;
  if (!placed() || !document.body) return stale;
  const simple = (e, attrs) => e.attributes.length <= attrs.length && e.getAttributeNames().every(k => attrs.includes(k));
  const tags = (e, tag) => e && e.namespaceURI === 'http://www.w3.org/1999/xhtml' && e.localName === tag && e.getRootNode() === document;
  const labelText = (el, input) => {
    if (el.childNodes.length > 3) return '';
    let text = '';
    for (const node of el.childNodes) {
      if (node === input) continue;
      if (node.nodeType !== Node.TEXT_NODE || node.length > 80) return '';
      text += node.data;
    }
    return text.trim();
  };
  const fieldNames = ['note', 'comment'];
  const plainAncestors = form => {
    let node=form.parentElement;
    for (let depth=0;node && depth<32;depth++,node=node.parentElement) {
      if (!['html','body','main','section','article','div'].includes(node.localName) || node.hasAttribute('is') || node.hasAttribute('popover')) return false;
      if (node===document.documentElement) return true;
    }
    return false;
  };
  const buttonNames = ['Save', 'Submit', 'Add note', 'Add comment'];
  const detect = form => {
    if (!tags(form, 'form') || !simple(form, ['id','class','action','method','autocomplete']) || form.method !== 'post' || form.enctype !== 'application/x-www-form-urlencoded' || form.action !== p.end || new URL(form.action).origin !== window.origin || document.querySelector('base[target]')) return null;
    if (!plainAncestors(form) || form.closest('[inert],[hidden],[contenteditable],[is],dialog') || form.elements.length !== 2 || form.children.length !== 2) return null;
    const label = form.children[0], button = form.children[1];
    if (!tags(label,'label') || !simple(label,['id','class']) || label.children.length !== 1 || !tags(button,'button')) return null;
    const field = label.firstElementChild;
    if (field.isContentEditable || field.matches(':-webkit-autofill') || form.closest('[aria-hidden=true]')) return null;
    if (!(tags(field,'input') && field.type === 'text' || tags(field,'textarea')) || !simple(field,['id','class','name','type','autocomplete','maxlength']) || !fieldNames.includes(field.name) || field.type === 'text' && field.hasAttribute('type') && field.getAttribute('type').toLowerCase() !== 'text') return null;
    if (field.localName === 'input' && getComputedStyle(field).webkitTextSecurity !== 'none') return null;
    if (field.autocomplete && field.autocomplete !== 'off' || field.matches(':disabled,:read-only') || !simple(button,['id','class','type']) || button.type !== 'submit' || button.matches(':disabled')) return null;
    if (form.elements[0] !== field || form.elements[1] !== button || field.form !== form || button.form !== form || field.labels.length !== 1 || field.labels[0] !== label) return null;
    const name = labelText(label,field), submit = labelText(button,null);
    if (name.toLowerCase() !== field.name || !buttonNames.includes(submit)) return null;
    return {form,field,button,label,name,submit};
  };
  const visible = el => {
    if (!el.isConnected || !el.checkVisibility({opacityProperty:true,visibilityProperty:true,contentVisibilityAuto:true})) return false;
    const b = el.getBoundingClientRect();
    if (b.width < 4 || b.height < 4 || b.left < 0 || b.top < 0 || b.right > innerWidth || b.bottom > innerHeight) return false;
    for (const [x,y] of [[.5,.5],[.15,.15],[.85,.15],[.15,.85],[.85,.85]]) {
      if (document.elementFromPoint(b.left+x*b.width,b.top+y*b.height) !== el) return false;
    }
    return true;
  };
  const controls = m => document.visibilityState === 'visible' && document.hasFocus() && [document.body,m.field,m.button].includes(document.activeElement) && visible(m.field) && visible(m.button);
  const cacheKey = '__sliccOrdinaryFormRefs';
  if (p.op === 'snapshot') {
    const records = new Map();
    Object.defineProperty(globalThis,cacheKey,{value:{document,records}, configurable:true,writable:true});
    const notes = [];
    if (document.forms.length <= 16) {
      for (const form of document.forms) {
        const m = detect(form);
        if (!m || !controls(m)) continue;
        for (const [el,op,role,name] of [[m.field,'fill','textbox',m.name],[m.button,'click','button',m.submit]]) {
          const ref = p.prefix + '_' + records.size;
          records.set(ref,{...m,el,op});
          notes.push(role+' '+JSON.stringify(name)+' [ref='+ref+']');
        }
      }
    }
    return {status:'ok',text:notes.join('\n'),count:records.size};
  }
  const cache = globalThis[cacheKey];
  const r = cache && cache.document === document && cache.records.get(p.ref);
  if (!r || r.op !== p.op) return stale;
  const m = detect(r.form);
  if (p.op === 'click') {
    const prepared = globalThis.__sliccPreparedNote;
    if (!prepared || prepared.lease !== p.tag || prepared.field !== r.field || prepared.button !== r.button || r.field.value !== prepared.value) return stale;
  }
  if (!m || m.field !== r.field || m.button !== r.button || m.label !== r.label || m.name !== r.name || m.submit !== r.submit || !controls(m) || !placed()) return stale;
  globalThis[cacheKey] = null;
  if (p.op === 'fill') {
    if (m.field.maxLength >= 0 && p.value.length > m.field.maxLength) return stale;
    const proto = m.field.localName === 'input' ? HTMLInputElement.prototype : HTMLTextAreaElement.prototype;
    Object.getOwnPropertyDescriptor(proto,'value').set.call(m.field,p.value);
    if (!placed() || !m.field.isConnected) return unsure;
    Object.defineProperty(globalThis,'__sliccPreparedNote',{value:{lease:p.tag,field:m.field,button:m.button,value:p.value},configurable:true});
    EventTarget.prototype.dispatchEvent.call(m.field,new Event('input',{bubbles:true}));
  } else {
    HTMLElement.prototype.click.call(m.button);
  }
  const after=detect(m.form);
  return placed() && after && after.field === m.field && after.button === m.button && controls(after) ? {status:'ok'} : unsure;
})()
