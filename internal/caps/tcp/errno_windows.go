//go:build windows

package tcp

import "syscall"

var errnos = []errnoMap{
	{"ECONNREFUSED", []error{syscall.Errno(10061), syscall.ECONNREFUSED}},
	{"ENETUNREACH", []error{syscall.Errno(10051), syscall.Errno(10050), syscall.ENETUNREACH}},
	{"EHOSTUNREACH", []error{syscall.Errno(10065), syscall.Errno(10064), syscall.EHOSTUNREACH}},
	{"ETIMEDOUT", []error{syscall.Errno(10060), syscall.ETIMEDOUT}},
	{"ECONNRESET", []error{syscall.Errno(10054), syscall.Errno(10053), syscall.ECONNRESET}},
}
