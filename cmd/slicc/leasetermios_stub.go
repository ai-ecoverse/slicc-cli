//go:build !(linux || darwin) || wasip1

package main

func termiosEcho(int) (bool, error) { return false, nil }
