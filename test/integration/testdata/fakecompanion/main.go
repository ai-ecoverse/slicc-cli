package main

import (
	"fmt"
	"os"
)

var name, version = "tool", "0.0.0"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	os.Exit(2)
}
