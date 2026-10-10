package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/ssh"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
)

const computeUsage = `  --runner <argv…> [--]
                       offer compute over SSH (the ssh capability): each
                       command runs as "<argv…> <command>", for example
                       --runner bash -c; the argv runs to a lone -- or the
                       end of the command line
  --shell <argv…> [--] the interactive shell for ssh sessions without a
                       command (default: the runner without its -c, plus -l
                       for POSIX shells: bash -c gives bash -l)
  --eval <argv…> [--]  a persistent REPL for the slicc-eval subsystem, for
                       example --eval python3 -i
  --eval-quiet <dur>   the silence that ends one eval response (default 500ms)
  --sftp, --no-sftp    serve sftp or not (default: on when the runner is a
                       plain local shell: bash, sh, zsh, fish, pwsh,
                       powershell or cmd)
`

var argvFlags = []string{"runner", "shell", "eval"}

type computeFlags struct {
	runner    []string
	shell     []string
	eval      []string
	evalQuiet time.Duration
	sftp      bool
	noSFTP    bool
}

func argvFlagName(arg string) (string, bool) {
	name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
	if name == arg {
		return "", false
	}
	base, _, hasValue := strings.Cut(name, "=")
	for _, f := range argvFlags {
		if base == f {
			if hasValue {
				return f + "=", true
			}
			return f, true
		}
	}
	return "", false
}

func extractCompute(args []string) ([]string, computeFlags, error) {
	var cf computeFlags
	targets := map[string]*[]string{"runner": &cf.runner, "shell": &cf.shell, "eval": &cf.eval}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, ok := argvFlagName(args[i])
		if !ok {
			rest = append(rest, args[i])
			continue
		}
		if strings.HasSuffix(name, "=") {
			base := strings.TrimSuffix(name, "=")
			return nil, cf, fmt.Errorf("--%s takes the argv as separate words: --%s bash -c", base, base)
		}
		if len(*targets[name]) > 0 {
			return nil, cf, fmt.Errorf("--%s given twice", name)
		}
		var argv []string
		for i++; i < len(args) && args[i] != "--"; i++ {
			argv = append(argv, args[i])
		}
		if len(argv) == 0 {
			return nil, cf, fmt.Errorf("--%s needs a command, for example --%s bash -c", name, name)
		}
		*targets[name] = argv
	}
	return rest, cf, nil
}

func (cf *computeFlags) register(fs *flag.FlagSet) {
	fs.DurationVar(&cf.evalQuiet, "eval-quiet", 0, "")
	fs.BoolVar(&cf.sftp, "sftp", false, "")
	fs.BoolVar(&cf.noSFTP, "no-sftp", false, "")
}

func (cf *computeFlags) validate() error {
	needs := map[string]bool{
		"--shell": len(cf.shell) > 0, "--eval": len(cf.eval) > 0, "--eval-quiet": cf.evalQuiet != 0,
		"--sftp": cf.sftp, "--no-sftp": cf.noSFTP,
	}
	if len(cf.runner) == 0 {
		for _, name := range []string{"--shell", "--eval", "--eval-quiet", "--sftp", "--no-sftp"} {
			if needs[name] {
				return fmt.Errorf("%s needs --runner", name)
			}
		}
	}
	switch {
	case cf.sftp && cf.noSFTP:
		return errors.New("--sftp and --no-sftp exclude each other")
	case cf.evalQuiet < 0:
		return errors.New("--eval-quiet must be positive")
	case cf.evalQuiet != 0 && len(cf.eval) == 0:
		return errors.New("--eval-quiet needs --eval")
	}
	return nil
}

func (cf *computeFlags) sftpOn() bool {
	if cf.noSFTP {
		return false
	}
	return cf.sftp || sshd.SFTPDefault(cf.runner)
}

func (cf *computeFlags) serve(n *node.Node, status func(string, ...any)) (func(), error) {
	if len(cf.runner) == 0 {
		return func() {}, nil
	}
	key, err := sshd.DefaultHostKey()
	if err != nil {
		return nil, fmt.Errorf("ssh host key: %w", err)
	}
	srv, err := sshd.New(sshd.Config{
		HostKey:   key,
		Runner:    cf.runner,
		Shell:     cf.shell,
		Eval:      cf.eval,
		EvalQuiet: cf.evalQuiet,
		Dialer:    n.Dialer(),
		NoSFTP:    !cf.sftpOn(),
	})
	if err != nil {
		return nil, err
	}
	ssh.Register(n, srv)
	status("%s", cf.summary())
	return func() { _ = srv.Close() }, nil
}

func (cf *computeFlags) summary() string {
	shell := cf.shell
	if shell == nil {
		shell = sshd.ShellFor(cf.runner)
	}
	parts := []string{"ssh: runner " + strings.Join(cf.runner, " ")}
	if len(shell) > 0 {
		parts = append(parts, "shell "+strings.Join(shell, " "))
	} else {
		parts = append(parts, "no shell")
	}
	if cf.sftpOn() {
		parts = append(parts, "sftp on")
	} else {
		parts = append(parts, "sftp off")
	}
	if len(cf.eval) > 0 {
		parts = append(parts, "eval "+strings.Join(cf.eval, " "))
	}
	return strings.Join(parts, ", ")
}
