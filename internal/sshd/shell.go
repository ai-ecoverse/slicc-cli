package sshd

import (
	"path/filepath"
	"strings"
)

var loginShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "fish": true,
}

func ShellFor(runner []string) []string {
	n := len(runner)
	if n < 2 {
		return nil
	}
	last := runner[n-1]
	base := append([]string{}, runner[:n-1]...)
	switch last {
	case "-c":
		if loginShells[shellName(runner[n-2])] {
			base = append(base, "-l")
		}
		return base
	case "/c", "/C", "-Command", "-command":
		return base
	}
	return nil
}

func ExecArgv(runner []string, command string) []string {
	return append(append([]string{}, runner...), command)
}

func shellName(arg string) string {
	name := strings.ToLower(filepath.Base(arg))
	return strings.TrimSuffix(name, ".exe")
}
