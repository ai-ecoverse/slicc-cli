//go:build unix && !linux && !darwin

package main

import "errors"

func termiosForceEcho(int) error { return errors.New("termios echo not supported") }
