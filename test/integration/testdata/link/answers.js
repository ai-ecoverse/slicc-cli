const keep = 12;
const textLimit = 160;
const settled = new Set(['pass', 'skip', 'not-run']);
const word = (value) => (typeof value === 'string' && /^[a-z]{1,16}$/.test(value) ? value : undefined);

export function answerLog() {
  const legs = {};
  let seq = 0;
  return {
    legs,
    record(leg, method, send) {
      const entry = { seq: ++seq, method, ok: null };
      const log = (legs[leg] ??= { sent: 0, last: [] });
      log.sent += 1;
      log.last.push(entry);
      if (log.last.length > keep) log.last.shift();
      const started = performance.now();
      const answered = (result) => {
        try {
          entry.ms = Math.round(performance.now() - started);
          entry.ok = true;
          const type = word(result?.result?.type);
          const subtype = word(result?.result?.subtype);
          if (type) entry.type = type;
          if (subtype) entry.subtype = subtype;
        } catch {
          return;
        }
      };
      const failed = (error) => {
        try {
          entry.ms = Math.round(performance.now() - started);
          entry.ok = false;
          entry.error = { message: String(error?.message ?? error).slice(0, textLimit) };
          if (Number.isInteger(error?.cdp?.code)) entry.error.code = error.cdp.code;
        } catch {
          return;
        }
      };
      let pending;
      try {
        pending = send();
      } catch (error) {
        failed(error);
        throw error;
      }
      if (typeof pending?.then !== 'function') answered(pending);
      else {
        try {
          pending.then(answered, failed);
        } catch {
          return pending;
        }
      }
      return pending;
    },
  };
}

function copy(entry) {
  const out = { seq: entry.seq, method: entry.method, ok: entry.ok };
  for (const key of ['ms', 'type', 'subtype']) if (entry[key] !== undefined) out[key] = entry[key];
  if (entry.error) out.error = entry.error.code === undefined ? { message: entry.error.message } : { message: entry.error.message, code: entry.error.code };
  return out;
}

export function attachAnswers(row, log) {
  if (!log || ['control', 'guarded', 'live'].every((leg) => settled.has(row[leg]))) return row;
  const evaluateAnswers = Object.fromEntries(Object.entries(log.legs).map(([leg, { sent, last }]) => [leg, { sent, last: last.map(copy) }]));
  row.diagnostics = { ...(row.diagnostics && typeof row.diagnostics === 'object' ? row.diagnostics : {}), evaluateAnswers };
  return row;
}
