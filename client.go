package grab

import (
	"fmt"
	"net/http"
	"time"
)

// CheckRedirectPreserveHeaders allows the preservation of request headers on redirects.
// See: https://github.com/golang/go/issues/4800
func CheckRedirectPreserveHeaders(limit int) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			// No redirects
			return nil
		}

		// Only allow GET redirection, prevent possibility of sending data to a malicious server.
		if req.Method != http.MethodGet {
			return fmt.Errorf("method %s redirection not allowed", req.Method)
		}

		if len(via) > limit {
			return fmt.Errorf("%d consecutive requests(redirects)", len(via))
		}

		// mutate the subsequent redirect requests with the first Header
		for key, val := range via[0].Header {
			req.Header[key] = val
		}
		return nil
	}
}

// ClientWithTimeout returns the recommended client to use with this package.
// It features a universal timeout and allows proxy configuration from the environment.
// If PreserveHeaders is true then the client will preserve headers across hostname boundaries.
func ClientWithTimeout(timeout time.Duration, PreserveHeaders bool) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   10,
	}
	c := &http.Client{
		Transport: transport,
	}
	if PreserveHeaders {
		c.CheckRedirect = CheckRedirectPreserveHeaders(5)
	}
	return c
}
