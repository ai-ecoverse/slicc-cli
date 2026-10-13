package main

import (
	"os"
	"strings"
)

func main() {
	f, err := os.OpenFile(os.Getenv("SLICC_FAKEOPEN_OUT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(1)
	}
	_, _ = f.WriteString(strings.Join(os.Args[1:], " ") + "\n")
	_ = f.Close()
}
