package cdp

import (
	"net/http"
	"sync/atomic"
)

var leaseCleanupTrace atomic.Pointer[func(string, []byte)]

func LeaseCleanupTrace(hook func(string, []byte)) func() {
	old := leaseCleanupTrace.Swap(&hook)
	return func() { leaseCleanupTrace.Store(old) }
}

func LeaseCleanupHTTPTransport(transport http.RoundTripper) func() {
	old := httpClient
	httpClient = &http.Client{Transport: transport}
	return func() { httpClient = old }
}

func leaseCleanupRecord(direction string, data []byte) {
	if hook := leaseCleanupTrace.Load(); hook != nil {
		(*hook)(direction, data)
	}
}
