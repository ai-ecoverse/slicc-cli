package browser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const ProcRootEnv = "SLICC_PROC_ROOT"

func procRoot() string {
	if v := os.Getenv(ProcRootEnv); v != "" {
		return v
	}
	return "/proc"
}

func procTable(root string) ([]byte, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := bytes.TrimRight(bytes.ReplaceAll(raw, []byte{0}, []byte{' '}), " ")
		if len(args) == 0 {
			continue
		}
		fmt.Fprintf(&out, "%d %s\n", pid, args)
	}
	return out.Bytes(), nil
}
