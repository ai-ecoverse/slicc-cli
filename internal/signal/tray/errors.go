package tray

import (
	"errors"
	"fmt"
)

const (
	CodeInvalidJoin           = "INVALID_JOIN_CAPABILITY"
	CodeTrayExpired           = "TRAY_EXPIRED"
	CodeSuperseded            = "TRAY_SUPERSEDED"
	CodeSupersededMissingJoin = "TRAY_SUPERSEDED_MISSING_JOIN_URL"
	CodeSupersedeExhausted    = "TRAY_SUPERSEDED_CHAIN_EXHAUSTED"
	CodeBootstrapNotFound     = "BOOTSTRAP_NOT_FOUND"
	CodeOfferTimeout          = "OFFER_TIMEOUT"
	CodeRequestTimeout        = "REQUEST_TIMEOUT"
	CodeNetwork               = "NETWORK"
	CodeBadResponse           = "BAD_RESPONSE"
	CodeState                 = "STATE"
)

var ErrClosed = errors.New("tray session closed")

type Error struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("tray hub: %s (%s)", e.Message, e.Code)
}

func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func IsRetryable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Retryable
}
