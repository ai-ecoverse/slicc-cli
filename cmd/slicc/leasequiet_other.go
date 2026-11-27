//go:build !((linux || darwin) && !wasip1)

package main

func quietDrainLeaseInput(int) {}
