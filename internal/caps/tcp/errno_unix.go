//go:build !windows

package tcp

import "syscall"

var errnos = []errnoMap{
	{"ECONNREFUSED", []error{syscall.ECONNREFUSED}},
	{"ENETUNREACH", []error{syscall.ENETUNREACH, syscall.ENETDOWN}},
	{"EHOSTUNREACH", []error{syscall.EHOSTUNREACH, syscall.EHOSTDOWN}},
	{"ETIMEDOUT", []error{syscall.ETIMEDOUT}},
	{"ECONNRESET", []error{syscall.ECONNRESET, syscall.EPIPE}},
}
