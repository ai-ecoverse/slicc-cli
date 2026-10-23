export const laterRefusal = 'slicc: refused: scripts and bindings for later documents could run in seven’s pages or the vault, so the guard doesn’t allow them; run a script in the page you see with Runtime.evaluate instead';

export function adaptLaterExitVector(set) {
  const vector = set.vectors.find((v) => v.id === 'X1.after-exit');
  if (!vector) return;
  const scripts = vector.setup.filter((s) => s.cdp === 'Page.addScriptToEvaluateOnNewDocument');
  const waits = vector.trigger.filter((s) => s.settle?.beacon === '{ordinary}');
  if (scripts.length !== 1 || waits.length !== 1 || vector.control.via !== 'guard' || vector.control.expected.instrumented !== true || vector.expected.instrumented !== true) throw new Error('X1.after-exit changed upstream; review the retired-script adaptation');
  const match = '^'+laterRefusal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')+'$';
  scripts[0].as = 'exitInit';
  scripts[0].refusable = true;
  scripts[0].refusal = { match };
  waits[0].settle.dependsOn = ['exitInit'];
  vector.observe.laterScriptRefused = { means: 'the retired init script received the exact terminal guard refusal before exit', check: { refused: ['exitInit'], refusal: { match }, observer: 'client' } };
  for (const expected of [vector.control.expected, vector.expected]) {
    expected.instrumented = false;
    expected.laterScriptRefused = true;
  }
}
