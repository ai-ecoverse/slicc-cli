package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const credentialExportDiagInject = `
  const scrub = (s) => String(s ?? '')
    .replace(/n=[^&\s"']+/g, 'n=<redacted>')
    .replace(/https?:\/\/[^\s"']+/g, '<url>')
    .slice(0, 240);
  const firstFail = Object.create(null);
  const stepOf = (m) => {
    if (m === 'Target.attachToTarget') return 'attach';
    if (m === 'Target.createTarget') return 'createTarget';
    if (m === 'Page.navigate') return 'navigate';
    if (m === 'Runtime.evaluate') return 'evaluate';
    if (
      m.startsWith('Network.') ||
      m.startsWith('Storage.') ||
      m === 'Page.getCookies' ||
      m === 'Page.deleteCookie'
    )
      return 'cookies';
    if (m.startsWith('DOMStorage.') || m.startsWith('IndexedDB.') || m.startsWith('CacheStorage.'))
      return 'storage';
    return 'other';
  };
  let evalCount = 0;
  const origSend = client.send.bind(client);
  client.send = async (method, params, session) => {
    const sessionRole = session ? 'page' : 'browser';
    try {
      const res = await origSend(method, params, session);
      if (method === 'Target.createTarget')
        note('diag.createTarget', { ok: true, sessionRole, targetType: 'page' });
      if (method === 'Page.navigate') {
        const errorText = scrub(res?.errorText ?? '');
        note('diag.navigate', {
          ok: !errorText,
          sessionRole,
          targetType: 'page',
          errorText,
        });
      }
      if (method === 'Runtime.evaluate' && evalCount++ === 0)
        note('diag.evaluate', { ok: true, sessionRole, targetType: 'page' });
      return res;
    } catch (error) {
      const code = error?.code ?? error?.response?.code ?? null;
      const text = scrub(error?.message ?? error);
      const step = stepOf(method);
      if (!firstFail[step]) {
        firstFail[step] = { method, sessionRole, targetType: 'page', code, text };
        note('diag.firstFail.' + step, firstFail[step]);
      }
      if (method === 'Target.createTarget')
        note('diag.createTarget', { ok: false, sessionRole, targetType: 'page', code, text });
      if (method === 'Page.navigate')
        note('diag.navigate', { ok: false, sessionRole, targetType: 'page', code, text });
      if (method === 'Runtime.evaluate' && evalCount++ === 0)
        note('diag.evaluate', { ok: false, sessionRole, targetType: 'page', code, text });
      throw error;
    }
  };
`

func stageCredentialExportDiag(t *testing.T) string {
	t.Helper()
	src := guardPackage(t)
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "credential-export.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	needle := "async function probeCredentialClient(ctx) {\n  const { client, internalSessions, targetId, result, note } = ctx;\n"
	if !strings.Contains(string(raw), needle) {
		t.Fatal("credential-export.mjs probeCredentialClient shape changed; update the diag inject")
	}
	patched := strings.Replace(string(raw), needle, needle+credentialExportDiagInject, 1)
	readerNeedle := "async function openPage(reader, url, notes, internalSessions) {\n  const opened = await outcome(\n    createObserverTarget(reader, { url }, notes).then(async (created) => {\n"
	readerInject := `async function openPage(reader, url, notes, internalSessions) {
  const opened = await outcome(
    createObserverTarget(reader, { url }, notes).then(async (created) => {
      notes.push('diag.reader.createTarget: ' + JSON.stringify({ ok: true, sessionRole: 'browser', targetType: 'page' }));
`
	if !strings.Contains(patched, readerNeedle) {
		t.Fatal("credential-export.mjs openPage shape changed; update the diag inject")
	}
	patched = strings.Replace(patched, readerNeedle, readerInject, 1)
	if err := os.WriteFile(path, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCDPCredentialExportDiag(t *testing.T) {
	pkg := stageCredentialExportDiag(t)
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{
				variant:    variant,
				slots:      2,
				only:       "C7.credential-export",
				rowTimeout: guardRowTimeout,
				packageDir: pkg,
				diagChrome: true,
			})
			if len(sum.Results) == 0 {
				t.Fatal("no C7.credential-export rows")
			}
			for _, r := range sum.Results {
				name := r.ID + " " + r.Target + " " + r.Variant
				t.Logf("DIAG %s: control=%s guarded=%s live=%s", name, r.Control, r.Guarded, r.Live)
				t.Logf("DIAG %s why=%s", name, r.Why)
				t.Logf("DIAG %s observed=%s", name, r.Observed)
				for _, n := range r.Notes {
					t.Logf("DIAG %s note: %s", name, n)
				}
				if r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass" {
					t.Errorf("%s: control %s, guarded %s, live %s (see DIAG notes above for firstFail / createTarget / navigate / evaluate)", name, r.Control, r.Guarded, r.Live)
				}
			}
		})
	}
}
