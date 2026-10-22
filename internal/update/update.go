package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/attest"
)

const (
	DefaultAPIBase = "https://api.github.com"
	Repo           = "ai-ecoverse/slicc-cli"
	APIBaseEnv     = "SLICC_UPDATE_API_BASE"
	ChecksumsName  = "SHA256SUMS"
	userAgent      = "slicc-cli"
	maxBinaryBytes = 512 << 20
	maxSumsBytes   = 1 << 20
	maxAttestBytes = 8 << 20
	ReleaseRef     = "refs/heads/main"
)

type Release struct {
	Version      string
	AssetName    string
	AssetURL     string
	ChecksumsURL string
}

type Checker struct {
	Repo         string
	Binary       string
	Ref          string
	Pipeline     bool
	Workflows    []string
	ChecksumOnly bool
	Root         *attest.TrustedRoot
	APIBase      string
	HTTP         *http.Client
	GOOS         string
	GOARCH       string
	Verify       func(ctx context.Context, path, version string) error
}

func NewChecker() *Checker {
	base := os.Getenv(APIBaseEnv)
	if base == "" {
		base = DefaultAPIBase
	}
	return &Checker{
		Repo:     Repo,
		Binary:   "slicc",
		Ref:      ReleaseRef,
		Pipeline: true,
		APIBase:  strings.TrimRight(base, "/"),
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		Verify:   VerifyVersion,
	}
}

func AssetName(goos, goarch string) string {
	return NamedAsset("slicc", goos, goarch)
}

func NamedAsset(name, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("%s-%s-%s.exe", name, goos, goarch)
	}
	return fmt.Sprintf("%s-%s-%s", name, goos, goarch)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	body, err := c.get(ctx, c.APIBase+"/repos/"+c.Repo+"/releases/latest", maxSumsBytes, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("asking GitHub for the latest release: %w", err)
	}
	var gh githubRelease
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, fmt.Errorf("decoding the latest release: %w", err)
	}
	if !IsReleaseVersion(gh.TagName) {
		return nil, fmt.Errorf("the latest release has an unexpected tag %q", gh.TagName)
	}
	rel := &Release{Version: Normalize(gh.TagName), AssetName: NamedAsset(c.Binary, c.GOOS, c.GOARCH)}
	for _, a := range gh.Assets {
		switch a.Name {
		case rel.AssetName:
			rel.AssetURL = a.URL
		case ChecksumsName:
			rel.ChecksumsURL = a.URL
		}
	}
	if rel.AssetURL == "" {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, rel.AssetName)
	}
	if rel.ChecksumsURL == "" {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, ChecksumsName)
	}
	return rel, nil
}

func (c *Checker) get(ctx context.Context, url string, limit int64, accept string) ([]byte, error) {
	res, err := c.open(ctx, url, accept)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	return body, nil
}

func (c *Checker) open(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}

func ParseChecksums(data []byte) map[string]string {
	sums := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums
}

func VerifyVersion(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "SLICC_NO_TELEMETRY=1", "SLICC_NO_UPDATE_CHECK=1")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("the downloaded binary failed to run --version: %w", err)
	}
	if got, want := strings.TrimSpace(string(out)), "slicc "+Normalize(version); got != want {
		return fmt.Errorf("the downloaded binary reports %q, expected %q", got, want)
	}
	return nil
}

func StagingPath(exe string) string {
	ext := filepath.Ext(exe)
	return strings.TrimSuffix(exe, ext) + ".new" + ext
}

func (c *Checker) download(ctx context.Context, rel *Release, dest string) error {
	sums, err := c.get(ctx, rel.ChecksumsURL, maxSumsBytes, "")
	if err != nil {
		return fmt.Errorf("downloading %s: %w", ChecksumsName, err)
	}
	want := ParseChecksums(sums)[rel.AssetName]
	if want == "" {
		return fmt.Errorf("%s has no entry for %s", ChecksumsName, rel.AssetName)
	}
	res, err := c.open(ctx, rel.AssetURL, "")
	if err != nil {
		return fmt.Errorf("downloading %s: %w", rel.AssetName, err)
	}
	defer res.Body.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(res.Body, maxBinaryBytes+1))
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return fmt.Errorf("downloading %s: %w", rel.AssetName, err)
	}
	if written > maxBinaryBytes {
		return fmt.Errorf("%s is larger than %d bytes", rel.AssetName, maxBinaryBytes)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		return fmt.Errorf("%s does not match its SHA-256 in %s", rel.AssetName, ChecksumsName)
	}
	if !c.ChecksumOnly {
		sumsDigest := sha256.Sum256(sums)
		if err := c.attested(ctx, rel.AssetName, got, hex.EncodeToString(sumsDigest[:])); err != nil {
			return err
		}
	}
	return os.Chmod(dest, 0o755)
}

func (c *Checker) Identity() attest.Identity {
	return attest.Identity{Repo: c.Repo, Ref: c.Ref, Pipeline: c.Pipeline, Workflows: c.Workflows}
}

func (c *Checker) attested(ctx context.Context, asset, digest, sumsDigest string) error {
	root := c.Root
	if root == nil {
		var err error
		if root, err = attest.DefaultRoot(); err != nil {
			return err
		}
	}
	err := c.attestedBy(ctx, root, asset, digest)
	if err == nil {
		return nil
	}
	if errors.Is(err, errNoAttestation) {
		if sumsErr := c.attestedBy(ctx, root, ChecksumsName, sumsDigest); sumsErr == nil {
			return nil
		} else if !errors.Is(sumsErr, errNoAttestation) {
			err = sumsErr
		}
	}
	return err
}

var errNoAttestation = errors.New("no attestation")

func (c *Checker) attestedBy(ctx context.Context, root *attest.TrustedRoot, name, digest string) error {
	body, err := c.get(ctx, c.APIBase+"/repos/"+c.Repo+"/attestations/sha256:"+digest, maxAttestBytes, "application/vnd.github+json")
	if err != nil {
		return fmt.Errorf("fetching the attestation of %s: %w (%w)", name, err, errNoAttestation)
	}
	var list struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list.Attestations) == 0 {
		return fmt.Errorf("%s has no attestation (%w)", name, errNoAttestation)
	}
	var last error
	for _, a := range list.Attestations {
		if last = attest.Verify(a.Bundle, digest, c.Identity(), root); last == nil {
			return nil
		}
	}
	return fmt.Errorf("no attestation of %s by %s verifies: %w", name, c.Identity(), last)
}

var rename = os.Rename

func (c *Checker) Apply(ctx context.Context, rel *Release, exe string) error {
	staging := StagingPath(exe)
	if err := c.download(ctx, rel, staging); err != nil {
		_ = os.Remove(staging)
		return err
	}
	if err := c.Verify(ctx, staging, rel.Version); err != nil {
		_ = os.Remove(staging)
		return err
	}
	return swap(c.GOOS, staging, exe)
}

func swap(goos, staging, exe string) error {
	parked := false
	if _, err := os.Stat(exe); goos == "windows" && err == nil {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := rename(exe, old); err != nil {
			_ = os.Remove(staging)
			return fmt.Errorf("moving the running binary aside: %w", err)
		}
		parked = true
	}
	if err := rename(staging, exe); err != nil {
		if parked {
			_ = rename(exe+".old", exe)
		}
		_ = os.Remove(staging)
		return fmt.Errorf("replacing the binary: %w", err)
	}
	return nil
}

func RemoveStale(exe string) {
	_ = os.Remove(exe + ".old")
}
