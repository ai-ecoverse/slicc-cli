package integration

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/attest"
)

var sliccRelease = attest.Identity{Repo: "ai-ecoverse/slicc-cli", Ref: "refs/heads/main"}

func realBundle(t *testing.T, version string) ([]byte, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "attest", "slicc-"+version+".bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.ReadFile(filepath.Join("testdata", "attest", "slicc-"+version+".sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return b, strings.TrimSpace(string(d))
}

func step(err error) string {
	var e *attest.Error
	if errors.As(err, &e) {
		return e.Step
	}
	if err == nil {
		return "ok"
	}
	return "other: " + err.Error()
}

type jsonMap = map[string]any

func mutate(t *testing.T, b []byte, fn func(m jsonMap)) []byte {
	t.Helper()
	var m jsonMap
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func entry(m jsonMap) jsonMap {
	return m["verificationMaterial"].(jsonMap)["tlogEntries"].([]any)[0].(jsonMap)
}

func flipB64(s string, at int) string {
	raw, _ := base64.StdEncoding.DecodeString(s)
	if at < 0 {
		at += len(raw)
	}
	raw[at] ^= 0x01
	return base64.StdEncoding.EncodeToString(raw)
}

func payload(m jsonMap) jsonMap {
	raw, _ := base64.StdEncoding.DecodeString(m["dsseEnvelope"].(jsonMap)["payload"].(string))
	var st jsonMap
	_ = json.Unmarshal(raw, &st)
	return st
}

func setPayload(m jsonMap, st jsonMap) {
	raw, _ := json.Marshal(st)
	m["dsseEnvelope"].(jsonMap)["payload"] = base64.StdEncoding.EncodeToString(raw)
}

func TestAttestRealReleases(t *testing.T) {
	root, err := attest.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v1.31.0", "v1.20.0"} {
		b, digest := realBundle(t, v)
		if err := attest.Verify(b, digest, sliccRelease, root); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	b, digest := realBundle(t, "v1.31.0")
	other, _ := realBundle(t, "v1.20.0")
	otherBody := entry(func() jsonMap { var m jsonMap; _ = json.Unmarshal(other, &m); return m }())["canonicalizedBody"]
	cases := []struct {
		name, want string
		digest     string
		id         attest.Identity
		fn         func(m jsonMap)
	}{
		{name: "another file's digest", want: "subject", digest: strings.Repeat("ab", 32)},
		{name: "the predicate type", want: "predicate", fn: func(m jsonMap) {
			st := payload(m)
			st["predicateType"] = "https://example.com/other/v1"
			setPayload(m, st)
		}},
		{name: "the subject digest in the statement", want: "subject", fn: func(m jsonMap) {
			st := payload(m)
			for _, s := range st["subject"].([]any) {
				s.(jsonMap)["digest"].(jsonMap)["sha256"] = strings.Repeat("0", 64)
			}
			setPayload(m, st)
		}},
		{name: "the statement, keeping its type", want: "dsse", fn: func(m jsonMap) {
			st := payload(m)
			st["predicate"] = jsonMap{"forged": true}
			setPayload(m, st)
		}},
		{name: "the DSSE signature", want: "dsse", fn: func(m jsonMap) {
			s := m["dsseEnvelope"].(jsonMap)["signatures"].([]any)[0].(jsonMap)
			s["sig"] = flipB64(s["sig"].(string), -3)
		}},
		{name: "the certificate's signature", want: "certificate", fn: func(m jsonMap) {
			c := m["verificationMaterial"].(jsonMap)["certificate"].(jsonMap)
			c["rawBytes"] = flipB64(c["rawBytes"].(string), -3)
		}},
		{name: "an integrated time after the leaf expired", want: "certificate", fn: func(m jsonMap) {
			e := entry(m)
			n, _ := strconv.ParseInt(e["integratedTime"].(string), 10, 64)
			e["integratedTime"] = strconv.FormatInt(n+3600, 10)
		}},
		{name: "another workflow ref", want: "identity", id: attest.Identity{Repo: "ai-ecoverse/slicc-cli", Ref: "refs/heads/dev"}},
		{name: "another repo", want: "identity", id: attest.Identity{Repo: "ai-ecoverse/slicc-jerk", Ref: "refs/heads/main"}},
		{name: "another entry's body", want: "tlog", fn: func(m jsonMap) { entry(m)["canonicalizedBody"] = otherBody }},
		{name: "the entry kind", want: "tlog", fn: func(m jsonMap) { entry(m)["kindVersion"].(jsonMap)["kind"] = "hashedrekord" }},
		{name: "an unknown log", want: "tlog", fn: func(m jsonMap) {
			entry(m)["logId"].(jsonMap)["keyId"] = base64.StdEncoding.EncodeToString(make([]byte, 32))
		}},
		{name: "the SET", want: "set", fn: func(m jsonMap) {
			p := entry(m)["inclusionPromise"].(jsonMap)
			p["signedEntryTimestamp"] = flipB64(p["signedEntryTimestamp"].(string), -3)
		}},
		{name: "the log index", want: "set", fn: func(m jsonMap) { entry(m)["logIndex"] = "1" }},
		{name: "no SET", want: "set", fn: func(m jsonMap) { delete(entry(m), "inclusionPromise") }},
		{name: "a proof hash", want: "inclusion", fn: func(m jsonMap) {
			h := entry(m)["inclusionProof"].(jsonMap)["hashes"].([]any)
			h[0] = flipB64(h[0].(string), 0)
		}},
		{name: "the proof's root hash", want: "inclusion", fn: func(m jsonMap) {
			p := entry(m)["inclusionProof"].(jsonMap)
			p["rootHash"] = flipB64(p["rootHash"].(string), 0)
		}},
		{name: "no inclusion proof", want: "inclusion", fn: func(m jsonMap) { delete(entry(m), "inclusionProof") }},
		{name: "the checkpoint signature", want: "checkpoint", fn: func(m jsonMap) {
			cp := entry(m)["inclusionProof"].(jsonMap)["checkpoint"].(jsonMap)
			env := cp["envelope"].(string)
			at := strings.LastIndex(env, " ")
			cp["envelope"] = env[:at+1] + flipB64(strings.TrimSpace(env[at+1:]), -3) + "\n"
		}},
		{name: "the checkpoint's tree size", want: "checkpoint", fn: func(m jsonMap) {
			cp := entry(m)["inclusionProof"].(jsonMap)["checkpoint"].(jsonMap)
			lines := strings.SplitN(cp["envelope"].(string), "\n", 3)
			lines[1] = "1"
			cp["envelope"] = strings.Join(lines, "\n")
		}},
		{name: "the bundle media type", want: "bundle", fn: func(m jsonMap) { m["mediaType"] = "application/vnd.dev.sigstore.bundle+json;version=0.1" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bb, d, id := b, digest, sliccRelease
			if c.fn != nil {
				bb = mutate(t, b, c.fn)
			}
			if c.digest != "" {
				d = c.digest
			}
			if c.id.Repo != "" {
				id = c.id
			}
			if got := step(attest.Verify(bb, d, id, root)); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestAttestGeneratedBundles(t *testing.T) {
	f := newFakeSigstore(t)
	t.Setenv(attest.RootEnv, f.rootPath)
	root, err := attest.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("c3", 32)
	san := releaseSAN("ai-ecoverse/slicc-cli")
	if err := attest.Verify(f.bundle(t, digest, bundleOptions{san: san}), digest, sliccRelease, root); err != nil {
		t.Fatalf("a well-formed bundle: %v", err)
	}
	for _, c := range []struct {
		name, want string
		o          bundleOptions
	}{
		{"another issuer", "identity", bundleOptions{san: san, issuer: "https://accounts.google.com"}},
		{"another workflow", "identity", bundleOptions{san: "https://github.com/ai-ecoverse/slicc-cli/.github/workflows/ci.yml@refs/heads/main"}},
		{"a branch ref", "identity", bundleOptions{san: "https://github.com/ai-ecoverse/slicc-cli/.github/workflows/release.yml@refs/heads/feature"}},
		{"an expired leaf", "certificate", bundleOptions{san: san, notAfter: -30 * time.Second}},
		{"no embedded SCT", "sct", bundleOptions{san: san, sct: "none"}},
		{"an SCT by another log key", "sct", bundleOptions{san: san, sct: "wrong-key"}},
	} {
		if got := step(attest.Verify(f.bundle(t, digest, c.o), digest, sliccRelease, root)); got != c.want {
			t.Fatalf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	real, d := realBundle(t, "v1.31.0")
	if got := step(attest.Verify(real, d, sliccRelease, root)); got != "certificate" {
		t.Fatalf("a real bundle under another trusted root: %s", got)
	}
}
