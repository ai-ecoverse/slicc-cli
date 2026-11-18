package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageAdobeComTopURLDiag(t *testing.T) string {
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
	needle := "async function plantPartitionedInTop(reader, { topUrl, iframeOrigin, lsKey, tag, until }) {\n  await outcome(reader.send('Target.setDiscoverTargets', { discover: true }));\n  const created = await outcome(reader.send('Target.createTarget', { url: topUrl }));\n  const topId = created.result?.targetId;\n  if (!topId) return { ok: false, storageKey: null, reportedKey: null };\n  const attach = await outcome(\n    reader.send('Target.attachToTarget', { targetId: topId, flatten: true })\n  );\n  const topSession = attach.result?.sessionId;\n  if (!topSession) return { ok: false, storageKey: null, reportedKey: null, topId };\n"
	inject := `async function plantPartitionedInTop(reader, { topUrl, iframeOrigin, lsKey, tag, until }) {
  await outcome(reader.send('Target.setDiscoverTargets', { discover: true }));
  const created = await outcome(reader.send('Target.createTarget', { url: topUrl }));
  const topId = created.result?.targetId;
  if (!topId) return { ok: false, storageKey: null, reportedKey: null };
  const attach = await outcome(
    reader.send('Target.attachToTarget', { targetId: topId, flatten: true })
  );
  const topSession = attach.result?.sessionId;
  if (!topSession) return { ok: false, storageKey: null, reportedKey: null, topId };
  const info = await outcome(reader.send('Target.getTargetInfo', { targetId: topId }));
  const got = String(info.result?.targetInfo?.url ?? '');
  const title = String(info.result?.targetInfo?.title ?? '').slice(0, 80);
  const asked = String(topUrl);
  if (typeof globalThis.__credPlantNotes === 'function')
    globalThis.__credPlantNotes('diag.topTarget', {
      askedScheme: asked.startsWith('https:') ? 'https' : asked.startsWith('http:') ? 'http' : 'other',
      askedHost: asked.replace(/^https?:\/\//, '').split('/')[0].split(':')[0],
      gotScheme: got.startsWith('https:') ? 'https' : got.startsWith('http:') ? 'http' : got.startsWith('chrome-error:') ? 'chrome-error' : 'other',
      gotHost: got.replace(/^https?:\/\//, '').split('/')[0].split(':')[0],
      title,
      createOk: created.ok === true,
      attachOk: Boolean(topSession),
    });
`
	if !strings.Contains(string(raw), needle) {
		t.Fatal("credential-export.mjs plantPartitionedInTop shape changed; update the diag inject")
	}
	patched := strings.Replace(string(raw), needle, inject, 1)
	noteHook := "const note = (label, evidence) => notes.push(`${label}: ${JSON.stringify(evidence)}`);\n"
	noteInject := "const note = (label, evidence) => notes.push(`${label}: ${JSON.stringify(evidence)}`);\n  globalThis.__credPlantNotes = note;\n"
	if !strings.Contains(patched, noteHook) {
		t.Fatal("credential-export.mjs note hook shape changed; update the diag inject")
	}
	patched = strings.Replace(patched, noteHook, noteInject, 1)
	pageGetNeedle := "  const pageGet = await tryMethod(client, 'Page.getCookies', {}, clientSession);\n  if (pageGet.skip) result.skips.push(pageGet.skip);\n"
	pageGetInject := `  const pageGet = await tryMethod(client, 'Page.getCookies', {}, clientSession);
  note('diag.pageGetCookies', {
    skip: pageGet.skip || null,
    ok: pageGet.out?.ok === true,
    code: pageGet.out?.error?.code ?? null,
    text: String(pageGet.out?.error?.message ?? '').slice(0, 160),
    refused: refused(pageGet.out),
  });
  if (pageGet.skip) result.skips.push(pageGet.skip);
`
	if !strings.Contains(patched, pageGetNeedle) {
		t.Fatal("credential-export.mjs Page.getCookies call shape changed; update the diag inject")
	}
	patched = strings.Replace(patched, pageGetNeedle, pageGetInject, 1)
	if err := os.WriteFile(path, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCDPCredentialExportAdobeComTopDiag(t *testing.T) {
	pkg := stageAdobeComTopURLDiag(t)
	sum := runGuard(t, guardRun{
		variant:    "prompt",
		slots:      2,
		only:       "C7.credential-export",
		rowTimeout: guardRowTimeout,
		packageDir: pkg,
	})
	if len(sum.Results) == 0 {
		t.Fatal("no C7.credential-export rows")
	}
	for _, r := range sum.Results {
		name := r.ID + " " + r.Target + " " + r.Variant
		for _, n := range r.Notes {
			if strings.Contains(n, "diag.topTarget") || strings.Contains(n, "diag.pageGetCookies") || strings.Contains(n, "partitioned:") {
				t.Logf("DIAG %s: %s", name, n)
			}
		}
		t.Logf("DIAG %s: control=%s guarded=%s why=%s", name, r.Control, r.Guarded, r.Why)
	}
}
