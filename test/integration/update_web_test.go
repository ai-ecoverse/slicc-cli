package integration

import (
	"net/http"
	"strings"
	"testing"
)

func (f *fakeGitHub) asked(path string) bool {
	_, ok := f.auth.Load(path)
	return ok
}

func TestUpdateTakesAReleaseWithItsBundleWithoutTheAPI(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"), withAttestations(nil), withSumsBundle(t, pipelineBundle("ai-ecoverse/slicc-cli")))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	if r := runBin(t, gh.env(t), bin, "update", "--check"); r.code != 0 || !strings.Contains(r.stdout, "slicc 1.1.0 is available (you have 1.0.0)") {
		t.Fatalf("--check: %+v", r)
	}
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || !strings.Contains(r.stdout, "to slicc 1.1.0") || versionOf(t, bin) != "slicc 1.1.0" {
		t.Fatalf("%+v", r)
	}
	if n := gh.api.Load(); n != 0 {
		t.Fatalf("%d requests to the GitHub API", n)
	}
}

func TestUpdateTakesTheAPIAttestationWhenTheReleaseHasNoBundle(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || versionOf(t, bin) != "slicc 1.1.0" {
		t.Fatalf("%+v", r)
	}
	if ok := gh.asked("/ai-ecoverse/slicc-cli/releases/download/v1.1.0/SHA256SUMS.sigstore.json"); !ok {
		t.Fatal("never looked for the release's bundle")
	}
	if ok := gh.asked("/repos/ai-ecoverse/slicc-cli/releases/latest"); ok {
		t.Fatal("asked the API for the latest release although github.com named it")
	}
}

func TestUpdateAsksTheAPIWhenGitHubDoesntNameTheLatestRelease(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"), withoutWeb)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || versionOf(t, bin) != "slicc 1.1.0" {
		t.Fatalf("%+v", r)
	}
	if ok := gh.asked("/repos/ai-ecoverse/slicc-cli/releases/latest"); !ok {
		t.Fatal("didn't fall back to the API")
	}
}

func TestUpdateVerifiesTheReleasesBundle(t *testing.T) {
	payload := newRelease(t, "1.1.0")
	for name, o := range map[string]bundleOptions{
		"a fork's ci.yml":     pipelineBundle("ai-ecoverse/slicc-cli-fork"),
		"release.yml at main": {san: releaseSAN("ai-ecoverse/slicc-cli")},
	} {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, "v1.1.0", payload, withAttestations(nil), withSumsBundle(t, o))
			bin := buildVersion(t, "1.0.0", t.TempDir())
			r := runBin(t, gh.env(t), bin, "update")
			if r.code != 1 || !strings.Contains(r.stderr, "SHA256SUMS.sigstore.json (the release's attestation of SHA256SUMS) doesn't verify") || !strings.Contains(r.stderr, "attestation identity") {
				t.Fatalf("%+v", r)
			}
			assertUntouched(t, bin)
		})
	}
	gh := newFakeGitHub(t, "v1.1.0", payload, withSumsBundle(t, pipelineBundle("ai-ecoverse/slicc-cli-fork")))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	if r := runBin(t, gh.env(t), bin, "update"); r.code != 0 || versionOf(t, bin) != "slicc 1.1.0" {
		t.Fatalf("a bad bundle kept the API's attestation from counting: %+v", r)
	}
}

func TestUpdateSendsTheTokenToTheAPIOnly(t *testing.T) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"), withoutWeb)
			bin := buildVersion(t, "1.0.0", t.TempDir())
			if r := runBin(t, gh.env(t, key+"=fake-token-0001"), bin, "update"); r.code != 0 {
				t.Fatalf("%+v", r)
			}
			seen := 0
			gh.auth.Range(func(k, v any) bool {
				path, auth := k.(string), v.(string)
				want := ""
				if strings.HasPrefix(path, "/repos/") {
					want, seen = "Bearer fake-token-0001", seen+1
				}
				if auth != want {
					t.Errorf("%s: Authorization %q, want %q", path, auth, want)
				}
				return true
			})
			if seen == 0 {
				t.Fatal("no request to the API")
			}
		})
	}
}

func TestUpdateNamesTheRateLimit(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", nil, withStatus(http.StatusForbidden))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "HTTP 403 (GitHub's API rate limit is used up; set GH_TOKEN or GITHUB_TOKEN for a higher one)") {
		t.Fatalf("%+v", r)
	}
	r = runBin(t, gh.env(t, "GH_TOKEN=fake-token-0001"), bin, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "rate limit is used up)") || strings.Contains(r.stderr, "fake-token") {
		t.Fatalf("with a token: %+v", r)
	}
}
