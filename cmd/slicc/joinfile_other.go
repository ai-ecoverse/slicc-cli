//go:build !unix

package main

import "os"

func privateFile(string, os.FileInfo) error { return nil }
