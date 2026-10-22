const mouseFrame = `new Promise((resolve) => {
  let frames = 0;
  let finished = false;
  const state = () => ({ visible: document.visibilityState === 'visible', focused: document.hasFocus(), frames });
  const finish = () => { if (!finished) { finished = true; resolve(state()); } };
  const timer = setTimeout(finish, 2000);
  const frame = () => {
    if (finished) return;
    const current = state();
    frames = current.visible && current.focused ? frames + 1 : 0;
    if (frames >= 2) { clearTimeout(timer); finish(); }
    else requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
})`;

export async function foregroundMouseCall(client, method, params = {}, sessionId) {
  if (!sessionId || method !== 'Input.dispatchMouseEvent') return client.send(method, params, sessionId);
  const evidence = { sessionId, type: params.type };
  client.mouseEvidence?.push(evidence);
  try {
    if (params.type !== 'mouseReleased') {
      evidence.focusReply = await client.send('Page.bringToFront', {}, sessionId);
      evidence.renderReply = await client.send('Runtime.evaluate', { expression: mouseFrame, awaitPromise: true, returnByValue: true }, sessionId);
      const ready = evidence.renderReply.result?.value;
      if (evidence.renderReply.exceptionDetails || !ready?.visible || !ready?.focused || ready.frames < 2) {
        throw new Error(`mouse target did not reach two focused visible frames: ${JSON.stringify(evidence.renderReply)}`);
      }
    }
    evidence.inputReply = await client.send(method, params, sessionId);
    return evidence.inputReply;
  } catch (error) {
    evidence.error = { message: error.message, cdp: error.cdp };
    throw error;
  }
}
