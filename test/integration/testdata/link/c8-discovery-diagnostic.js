const lower = (value) => String(value ?? '').toLowerCase();
const plain = (value) => lower(String(value ?? '').replace(/%([0-9a-f]{2})/gi, (_, hex) => String.fromCharCode(Number.parseInt(hex, 16))));
const layers = (value) => {
  const out = [lower(value)];
  for (let next = plain(out[0]); next !== out.at(-1); next = plain(next)) out.push(next);
  return out;
};
const parsed = (value) => { try { return new URL(value); } catch { return undefined; } };
const unschemed = (value) => value.replace(/^[a-z][a-z\d+.-]*:\/\//, '');
const originOnly = (value) => parsed(value)?.origin ?? (String(value ?? '').split('/')[0] || '');

export function diagnoseC8(client) {
  const send = client.send.bind(client);
  let targetId, trigger, failedURL, snapshot;
  client.send = (method, params, session) => {
    if (method === 'Target.attachToTarget') targetId = params.targetId;
    if (method === 'Page.navigate' && parsed(params.url)?.searchParams.has('to')) {
      trigger = params.url;
      failedURL = parsed(trigger).searchParams.get('to');
    }
    if (method === 'Page.reload' && !snapshot) snapshot = client.events.slice();
    return send(method, params, session);
  };
  return () => {
    const failed = parsed(failedURL);
    if (!failed || !snapshot) return { missing: true };
    const tag = failed.searchParams.get('query-secret');
    const parts = [failedURL, `${failed.pathname}${failed.search}${failed.hash}`, failed.pathname, failed.search.slice(1), failed.hash.slice(1), tag].filter((part) => part && part !== '/').map(plain);
    const leaks = (value) => layers(value).some((form) => parts.some((part) => form.includes(part)));
    const names = (value) => layers(value).some((form) => form.includes(failed.hostname)) || parsed(value)?.origin === failed.origin;
    const cleanTitle = (title) => !leaks(title) && (!names(title) || [failed.hostname, failed.host, failed.origin].includes(lower(title)));
    const ownTitle = ({ url, title }) => plain(title) !== '' && [plain(url), unschemed(plain(url))].includes(plain(title));
    const cleanURL = (url) => !leaks(url) && (lower(url) === failed.origin || (parsed(url)?.origin !== failed.origin && parsed(url)?.hostname !== failed.hostname));
    const home = parsed(trigger)?.href;
    return snapshot.filter((e) => ['Target.targetCreated', 'Target.targetInfoChanged'].includes(e.method)).map((e) => {
      const info = e.params?.targetInfo ?? {};
      const own = info.targetId === targetId;
      const triggerPage = own && parsed(info.url)?.href === home;
      const titleOK = triggerPage ? ownTitle(info) || cleanTitle(info.title) : cleanTitle(info.title) || (ownTitle(info) && !leaks(info.title));
      const failedFields = own ? {
        ...(!triggerPage && !cleanURL(info.url) ? { url: info.url } : {}),
        ...(!titleOK ? { title: info.title } : {}),
      } : layers(JSON.stringify(e.params ?? null)).some((form) => form.includes(plain(tag))) ? { params: e.params } : {};
      return { method: e.method, targetId: info.targetId, targetInfo: { url: originOnly(info.url), title: originOnly(info.title) }, failedFields };
    });
  };
}
