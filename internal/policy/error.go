package policy

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

const Errno = "EACCES"

var ErrDenied = errors.New("denied by policy")

type Error struct {
	Addr    netip.AddrPort
	Checked netip.Addr
	Rule    string
	Reason  string
}

func (e *Error) Error() string {
	target := e.Addr.String()
	if !e.Addr.IsValid() {
		target = "address"
	}
	if e.Checked.IsValid() && e.Checked != e.Addr.Addr() {
		target += " (as " + e.Checked.String() + ")"
	}
	switch {
	case e.Rule != "":
		return fmt.Sprintf("policy denies %s: deny %s", target, e.Rule)
	case e.Reason != "":
		return fmt.Sprintf("policy denies %s: %s", target, e.Reason)
	default:
		return "policy denies " + target
	}
}

func (e *Error) Errno() string { return Errno }

func (e *Error) Is(target error) bool {
	return target == ErrDenied || target == syscall.EACCES
}
