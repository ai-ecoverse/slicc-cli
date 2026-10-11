//go:build !windows

package main

func enableVT(any) bool { return true }
