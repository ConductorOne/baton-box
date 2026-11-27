package box

import (
	"net/http"
	"testing"
)

// The transfer request must not inherit uhttp's 60s response-header timeout: Box
// holds that request open while it moves content, and only the caller's context may
// bound the wait.
func TestNewTransferHTTPClient_NoHeaderTimeout(t *testing.T) {
	c := NewTransferHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want 0", tr.ResponseHeaderTimeout)
	}
	if c.Timeout != 0 {
		t.Errorf("client Timeout = %v, want 0", c.Timeout)
	}
	if tr.Proxy == nil {
		t.Error("Proxy is nil, want proxy settings from the environment")
	}
}
