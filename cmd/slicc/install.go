package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/fence"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/telemetry"
	"github.com/ai-ecoverse/slicc-cli/internal/update"
)

const installUsage = `usage: slicc install jerk|fnox [--checksum-only]

Installs a companion tool into slicc's own data folder, where slicc finds it:
  jerk   slicc-jerk, the latest release of ai-ecoverse/slicc-jerk (0.6.0 on)
  fnox   fnox, the latest release of ai-ecoverse/fnox-build

slicc downloads the release binary for this OS and CPU, checks it against
the release's SHA256SUMS and verifies its GitHub build provenance
attestation (signed by the repo's release.yml on main), then installs it
once it answers --version. --checksum-only skips the attestation, with a
warning. Run it again to update. Nothing on a link can start an install.
`

type companion struct {
	repo, binary, since string
}

var installable = map[string]companion{
	"jerk": {repo: "ai-ecoverse/slicc-jerk", binary: "jerk", since: jerk.MinVersion},
	"fnox": {repo: "ai-ecoverse/fnox-build", binary: "fnox"},
}

var versionRE = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

func runInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, installUsage)
		return 0
	}
	checksumOnly := len(args) == 2 && args[1] == "--checksum-only"
	if checksumOnly {
		args = args[:1]
	}
	if len(args) != 1 || installable[args[0]].repo == "" {
		fmt.Fprintf(stderr, "slicc install: name jerk or fnox\n\n%s", installUsage)
		return 2
	}
	c := installable[args[0]]
	dir, err := fence.DataDir()
	if err != nil {
		fmt.Fprintf(stderr, "slicc install: %v\n", err)
		return 1
	}
	dest := filepath.Join(dir, "bin", c.binary+exeSuffix())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	checker := update.NewChecker()
	checker.Repo, checker.Binary, checker.Verify, checker.ChecksumOnly = c.repo, c.binary, answersVersion, checksumOnly
	if checksumOnly {
		fmt.Fprintln(stderr, "slicc install: warning: --checksum-only skips the attestation; only SHA256SUMS is checked")
	}
	rel, err := checker.Latest(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "slicc install: %s: %s\n", c.repo, err)
		telemetry.ReportError(telemetry.Install, err)
		return 1
	}
	if c.since != "" && update.IsNewer(c.since, rel.Version) {
		fmt.Fprintf(stderr, "slicc install: %s %s is older than %s, the first release slicc works with\n", c.repo, rel.Version, c.since)
		return 1
	}
	if have := installedVersion(ctx, dest); have == rel.Version {
		fmt.Fprintf(stdout, "%s %s is already installed at %s\n", c.binary, have, dest)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(stderr, "slicc install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installing %s %s from %s\n", c.binary, rel.Version, c.repo)
	if err := checker.Apply(ctx, rel, dest); err != nil {
		fmt.Fprintf(stderr, "slicc install: %s\n", err)
		telemetry.ReportError(telemetry.Install, err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s %s at %s (%s)\n", c.binary, rel.Version, dest, verified(checker))
	return 0
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func installedVersion(ctx context.Context, path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	return versionRE.FindString(string(out))
}

func answersVersion(ctx context.Context, path, version string) error {
	if got := installedVersion(ctx, path); got != update.Normalize(version) {
		return fmt.Errorf("the downloaded binary reports version %q, expected %s", got, update.Normalize(version))
	}
	return nil
}

func companionsOnPath() {
	dir, err := fence.DataDir()
	if err != nil {
		return
	}
	bin := filepath.Join(dir, "bin")
	if fi, err := os.Stat(bin); err != nil || !fi.IsDir() {
		return
	}
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == bin {
			return
		}
	}
	if path != "" {
		path += string(os.PathListSeparator)
	}
	_ = os.Setenv("PATH", path+bin)
}
