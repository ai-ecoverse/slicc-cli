package main

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"time"
)

const (
	followBackoffBase = time.Second
	followBackoffMax  = 30 * time.Second
)

func jitter(d time.Duration) time.Duration {
	return d*3/4 + time.Duration(rand.Int64N(int64(d)/2+1))
}

func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://")
}

const maxJoinFile = 64 << 10

func isJoinRef(arg string) bool {
	return arg == "-" || (strings.HasPrefix(arg, "@") && len(arg) > 1)
}

func readJoinRef(arg string, stdin io.Reader) (string, error) {
	if arg == "-" || arg == "@-" {
		line, err := readLine(stdin)
		if err != nil {
			return "", fmt.Errorf("can't read the join URL from stdin: %w", err)
		}
		if line == "" {
			return "", errors.New("no join URL on stdin")
		}
		return line, nil
	}
	path := arg[1:]
	link, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("can't read the join URL: %w", err)
	}
	if !link.Mode().IsRegular() {
		return "", fmt.Errorf("%s isn't a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("can't read the join URL: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !os.SameFile(link, fi) {
		return "", fmt.Errorf("%s changed while slicc read it", path)
	}
	if err := privateFile(path, fi); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxJoinFile+1))
	if err != nil {
		return "", fmt.Errorf("can't read the join URL from %s", path)
	}
	if len(data) > maxJoinFile {
		return "", fmt.Errorf("%s is too big to hold a join URL", path)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("no join URL in %s", path)
}

func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	var c [1]byte
	for b.Len() <= maxJoinFile {
		n, err := r.Read(c[:])
		if n == 1 {
			if c[0] == '\n' {
				if s := strings.TrimSpace(b.String()); s != "" {
					return s, nil
				}
				b.Reset()
				continue
			}
			b.WriteByte(c[0])
		}
		if errors.Is(err, io.EOF) {
			return strings.TrimSpace(b.String()), nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("the first line is too long to be a join URL")
}
