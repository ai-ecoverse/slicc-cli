package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"

	hostfs "github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
)

var sixFlags = []string{
	"-v",
	"--serve-only",
	"--hosted",
	"--kill",
	"--install-cli",
	"--computer-demo",
	"--cdp-port",
	"--log-level",
	"--log-dir",
	"--prompt",
	"--env-file",
	"--install-dir",
	"--lead",
	"--join",
	"--electron-app",
	"--electron",
	"--cloud",
}

func sixFlagOf(arg string) string {
	for _, f := range sixFlags {
		if arg == f || (strings.HasPrefix(f, "--") && strings.HasPrefix(arg, f+"=")) {
			return f
		}
	}
	return ""
}

func sixFlagIn(args []string) string {
	for _, arg := range args {
		if arg == "--" {
			return ""
		}
		if f := sixFlagOf(arg); f != "" {
			return f
		}
	}
	return ""
}

type sixHit struct {
	flag string
	seen *string
}

func (s sixHit) String() string   { return "" }
func (s sixHit) IsBoolFlag() bool { return true }
func (s sixHit) Set(string) error {
	if *s.seen == "" {
		*s.seen = s.flag
	}
	return nil
}

func registerSix(fs *flag.FlagSet, seen *string) {
	for _, f := range sixFlags {
		fs.Var(sixHit{flag: f, seen: seen}, strings.TrimLeft(f, "-"), "")
	}
}

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(arg string) string {
	if safeArg.MatchString(arg) {
		return arg
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func sixCommand(args []string) string {
	out := make([]string, len(args))
	for i, arg := range args {
		if strings.Contains(arg, "/join/") {
			out[i] = "<join-url>"
			if at := strings.Index(arg, "="); strings.HasPrefix(arg, "--") && at > 0 && !strings.Contains(arg[:at], "/join/") {
				out[i] = arg[:at+1] + "<join-url>"
			}
			continue
		}
		out[i] = shellQuote(arg)
	}
	return strings.TrimSpace("npx sliccy@6 " + strings.Join(out, " "))
}

func sixPointer(stderr io.Writer, flag string, args []string) int {
	fmt.Fprintf(stderr, "slicc: %s is a SLICC six option. This is the new SLICC (seven). For SLICC six, run: %s\n", flag, sixCommand(args))
	return 2
}

func sixMount(spec string) (string, bool) {
	rest := strings.TrimSuffix(spec, ":ro")
	if _, name, _ := hostfs.ParseSpec(spec); name != "" {
		return "", false
	}
	at := strings.LastIndex(rest, ":")
	if at <= 0 {
		return "", false
	}
	head, tail := rest[:at], strings.TrimSpace(rest[at+1:])
	if !strings.HasPrefix(tail, "/") || isDriveLetter(head) || isDir(rest) {
		return "", false
	}
	example := head
	if base := path.Base(tail); base != "/" && base != "." && base != ".." {
		example += ":" + base
	}
	if strings.HasSuffix(spec, ":ro") {
		example += ":ro"
	}
	return example, true
}

func isDriveLetter(s string) bool {
	return len(s) == 1 && (s[0]|0x20) >= 'a' && (s[0]|0x20) <= 'z'
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func checkMounts(specs []string, prog string, stderr io.Writer) int {
	for _, spec := range specs {
		if example, ok := sixMount(spec); ok {
			fmt.Fprintf(stderr, "%s: --mount %s is SLICC six's form, <os-path>:<vfs-path>. This is the new SLICC (seven): use --mount <path>[:<name>][:ro], such as --mount %s, then mount the folder in seven.\n", prog, spec, shellQuote(example))
			return 2
		}
	}
	return -1
}
