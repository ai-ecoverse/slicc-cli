package main

import (
	"encoding/json"
	"os"
	"os/exec"
)

func main() {
	out := map[string]string{"launcher": os.Getenv("SLICC_LAUNCHER")}
	out["jerk"], _ = exec.LookPath("jerk")
	out["path"] = os.Getenv("PATH")
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
