package cdp

import (
	"net/http"
	"time"
)

var endpointTransport = newEndpointTransport()

func newEndpointTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DisableCompression = true
	return t
}

func EndpointClient(rt http.RoundTripper, timeout time.Duration) *http.Client {
	if rt == nil {
		rt = endpointTransport
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var endpointClient = EndpointClient(nil, 0)
