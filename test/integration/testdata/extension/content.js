globalThis.sliccFixtureWorld = { secret: 'fake-value-0001' };
document.documentElement?.setAttribute('data-slicc-world', '');
if (window === top) console.log('slicc fixture world', globalThis.sliccFixtureWorld);
document.addEventListener('slicc-ping', function sliccFixtureBusy() {
  const end = performance.now() + 20;
  while (performance.now() < end);
  document.dispatchEvent(new CustomEvent('slicc-pong'));
});
