package sshd

import "strings"

var loginShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "fish": true,
}

var plainShells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "pwsh": true, "powershell": true, "cmd": true,
}

func SFTPDefault(runner []string) bool {
	return len(runner) > 0 && plainShells[shellName(runner[0])]
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
	name := strings.ToLower(arg[strings.LastIndexAny(arg, `/\`)+1:])
	return strings.TrimSuffix(name, ".exe")
}
