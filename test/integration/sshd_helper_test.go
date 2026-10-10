package integration

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	helperFlag = "-sshd-helper"
	replFlag   = "-sshd-repl"
)

func init() {
	if len(os.Args) < 2 || (os.Args[1] != helperFlag && os.Args[1] != replFlag) {
		_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
		return
	}
	switch os.Args[1] {
	case helperFlag:
		if len(os.Args) >= 4 && os.Args[2] == "-c" {
			os.Exit(helperRun(os.Args[3]))
		}
		os.Exit(helperInteractive())
	case replFlag:
		os.Exit(helperREPL())
	}
}

func helperRunner() []string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return []string{exe, helperFlag, "-c"}
}

func helperREPLArgv() []string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return []string{exe, replFlag}
}

func helperRun(script string) int {
	for _, part := range strings.Split(script, ";") {
		if code, done := helperCommand(strings.Fields(part)); done {
			return code
		}
	}
	return 0
}

func helperCommand(words []string) (int, bool) {
	if len(words) == 0 {
		return 0, false
	}
	args := strings.Join(words[1:], " ")
	switch words[0] {
	case "echo":
		fmt.Println(args)
	case "stderr":
		fmt.Fprintln(os.Stderr, args)
	case "exit":
		code, _ := strconv.Atoi(args)
		return code, true
	case "cat":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "env":
		fmt.Println(os.Getenv(args))
	case "pwd":
		dir, _ := os.Getwd()
		fmt.Println(dir)
	case "tty":
		fmt.Printf("tty=%v\n", term.IsTerminal(int(os.Stdin.Fd())))
	case "size":
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		fmt.Printf("size=%dx%d err=%v\n", w, h, err)
	case "sleep":
		secs, _ := strconv.Atoi(args)
		time.Sleep(time.Duration(secs) * time.Second)
	default:
		fmt.Fprintf(os.Stderr, "helper: unknown command %q\n", words[0])
		return 127, true
	}
	return 0, false
}

func helperInteractive() int {
	fmt.Print("ready\n")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimRight(in.Text(), "\r")
		if code, done := helperCommand(strings.Fields(line)); done {
			return code
		}
	}
	return 0
}

func helperREPL() int {
	vars := map[string]string{}
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		words := strings.Fields(in.Text())
		switch {
		case len(words) == 3 && words[0] == "set":
			vars[words[1]] = words[2]
		case len(words) == 2 && words[0] == "get":
			fmt.Printf("%s=%s\n", words[1], vars[words[1]])
		case len(words) == 1 && words[0] == "quit":
			return 3
		case len(words) > 0:
			fmt.Fprintf(os.Stderr, "repl: %s?\n", words[0])
		}
	}
	return 0
}
