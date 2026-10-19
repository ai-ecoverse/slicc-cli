globalThis.sliccFixtureWorld = { secret: 'fake-value-0001' };
document.documentElement?.setAttribute('data-slicc-world', '');
if (window === top) console.log('slicc fixture world', globalThis.sliccFixtureWorld);
