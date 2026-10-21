package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	args := os.Args[1:]
	store := os.Getenv("FAKECLIP_FILE")
	logCall(name, args)
	mode, _ := os.ReadFile(store + ".mode")
	switch strings.TrimSpace(string(mode)) {
	case "fail":
		fmt.Fprintln(os.Stderr, "fakeclip: boom")
		os.Exit(1)
	case "deny":
		fmt.Fprintln(os.Stderr, "fakeclip: Permission denied")
		os.Exit(1)
	case "empty":
		fmt.Fprintln(os.Stderr, map[string]string{"wl-paste": "Nothing is copied", "xclip": "Error: target UTF8_STRING not available"}[name])
		os.Exit(1)
	case "big":
		if paste(name, args) {
			_, _ = os.Stdout.Write([]byte(strings.Repeat("a", 1<<20+1)))
			return
		}
	case "bad":
		if paste(name, args) {
			_, _ = os.Stdout.Write([]byte{'o', 'k', 0xff})
			return
		}
	}
	if paste(name, args) {
		b, _ := os.ReadFile(store)
		_, _ = os.Stdout.Write(b)
		return
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(store, b, 0o600); err != nil {
		os.Exit(1)
	}
}

func paste(name string, args []string) bool {
	switch name {
	case "pbpaste", "wl-paste":
		return true
	case "xclip":
		return slices.Contains(args, "-out")
	case "xsel":
		return slices.Contains(args, "--output")
	}
	return false
}

func logCall(name string, args []string) {
	log := os.Getenv("FAKECLIP_LOG")
	if log == "" {
		return
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, _ := json.Marshal(map[string]any{"name": name, "args": args, "lc_all": os.Getenv("LC_ALL"), "lang": os.Getenv("LANG")})
	_, _ = f.Write(append(b, '\n'))
}
