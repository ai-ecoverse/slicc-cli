//go:build !linux && !darwin

package main

func flushLeaseInput(int) {}
