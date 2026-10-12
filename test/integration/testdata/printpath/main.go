package main

import (
	"encoding/json"
	"os"
	"os/exec"
)

func main() {
	out := map[string]string{"launcher": os.Getenv("SLICC_LAUNCHER")}
	for _, name := range []string{"jerk", "fnox"} {
		p, _ := exec.LookPath(name)
		out[name] = p
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
