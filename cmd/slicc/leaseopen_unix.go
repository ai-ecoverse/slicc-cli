//go:build unix && !wasip1

package main

import "syscall"

const leaseNoFollow = syscall.O_NOFOLLOW
