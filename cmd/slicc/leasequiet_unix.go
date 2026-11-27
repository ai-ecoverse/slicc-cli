//go:build (linux || darwin) && !wasip1

package main

import (
	"time"

	"golang.org/x/sys/unix"
)

func quietDrainLeaseInput(fd int) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			flushLeaseInput(fd)
			return
		}
		wait := 300 * time.Millisecond
		if left < wait {
			wait = left
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := quietDrainPoll(fds, int(wait/time.Millisecond))
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n == 0 {
			return
		}
		re := fds[0].Revents
		if re&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return
		}
		if re&unix.POLLIN == 0 {
			return
		}
		flushLeaseInput(fd)
	}
}

var quietDrainPoll = unix.Poll
