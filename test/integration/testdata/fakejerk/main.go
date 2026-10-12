package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v := os.Getenv("FAKEJERK_VERSION")
		if v == "" {
			v = "0.3.1"
		}
		fmt.Println("jerk " + v)
		return
	}
	if len(args) == 1 && args[0] == "__hold" {
		time.Sleep(20 * time.Second)
		return
	}
	logCall(args)
	if len(args) == 2 && args[0] == "list" && args[1] == "--json" {
		fmt.Println(`[{"provider":"github","name":"GitHub (gh)","env":["GH_TOKEN","GITHUB_TOKEN"],"cli":true,"source":"built-in","token":"gho_secret_list"}]`)
		return
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	os.Exit(provider(args[0], strings.Join(args[1:], " ")))
}

func logCall(args []string) {
	log := os.Getenv("FAKEJERK_LOG")
	if log == "" {
		return
	}
	line, _ := json.Marshal(map[string]any{"args": args, "duration": os.Getenv("FNOX_LEASE_DURATION"), "lease": os.Getenv("FNOX_LEASE_ID")})
	f, err := os.OpenFile(filepath.Clean(log), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
}

func provider(p, rest string) int {
	switch p {
	case "broken":
		fmt.Fprintln(os.Stderr, "jerk: broken: sign in with jerk broken create gho_secret_stderr")
		fmt.Println(`{"message":"sign in with jerk broken create"}`)
		return 3
	case "dies":
		return 99
	case "orphan":
		self, _ := os.Executable()
		child := exec.Command(self, "__hold")
		child.Stdout = os.Stdout
		_ = child.Start()
		fmt.Println(`{"message":"left a child holding stdout"}`)
		return 0
	case "slow":
		time.Sleep(4 * time.Second)
	case "nolease":
		fmt.Println(`{"message":"nothing to lease"}`)
		return 0
	case "noexpiry":
		fmt.Println(`{"credentials":{"GH_TOKEN":"gho_secret_noexpiry"},"lease_id":"x"}`)
		return 0
	}
	return verb(p, rest)
}

func verb(p, rest string) int {
	switch {
	case rest == "get --json":
		fmt.Println(`{"provider":"` + p + `","user":"octocat","scopes":["repo"],"expires_at":"2030-01-01T00:00:00Z","token":"gho_…1234","raw":"gho_secret_get"}`)
	case rest == "list --json":
		fmt.Println(`{"items":[{"user":"octocat","id":"42","scopes":["repo"],"token":"gho_secret_accounts"}]}`)
	case strings.HasPrefix(rest, "create --via-link"):
		fmt.Println(`{"event":"progress","message":"sign-in opened in Google Chrome"}`)
		fmt.Println(`{"event":"progress","message":"waiting for the sign-in"}`)
		fmt.Printf("{\"user\":\"octocat\",\"message\":%q}\n", "created with "+rest)
	case rest == "lease --via-link":
		exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		fmt.Printf(`{"credentials":{"GH_TOKEN":"gho_secret_%[1]s","GITHUB_TOKEN":"gho_secret_%[1]s"},"expires_at":%q,"lease_id":"jerk-%[1]s-1"}`+"\n", p, exp)
		fmt.Println(`{"user":"octocat","scopes":["repo"]}`)
	case rest == "drop --lease --via-link":
		fmt.Println(`{"message":"dropped ` + os.Getenv("FNOX_LEASE_ID") + `"}`)
	default:
		fmt.Fprintln(os.Stderr, "jerk: unknown "+rest)
		return 2
	}
	return 0
}
