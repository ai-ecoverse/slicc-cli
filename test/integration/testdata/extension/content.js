globalThis.sliccFixtureWorld = { secret: 'fake-value-0001' };
document.documentElement?.setAttribute('data-slicc-world', '');
if (window === top) console.log('slicc fixture world', globalThis.sliccFixtureWorld.secret);
document.addEventListener('slicc-ping', function sliccFixtureBusy() {
  console.profile('slicc-fixture-profile');
  const end = performance.now() + 20;
  while (performance.now() < end);
  console.profileEnd('slicc-fixture-profile');
  document.dispatchEvent(new CustomEvent('slicc-pong'));
  if (window === top && !globalThis.sliccFixtureFetched) {
    globalThis.sliccFixtureFetched = true;
    fetch('/?n=extension-fetch&account=fake-value-0002', { method: 'POST', body: 'fake-value-0003', headers: { 'x-slicc-fixture': 'fake-value-0004' } })
      .then((r) => r.text())
      .then(() => document.documentElement.setAttribute('data-slicc-fetched', ''), () => document.documentElement.setAttribute('data-slicc-fetched', 'failed'));
    new WebSocket(`ws://${location.host}/ws`).onopen = (e) => e.target.close();
    const node = document.createElement('div');
    node.id = 'slicc-extension-node';
    document.body.append(node);
  }
});
