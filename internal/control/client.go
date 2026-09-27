package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
)

var errNotLoopback = errors.New("control_url is not a loopback address")

// noRedirectClient never follows redirects, so a capability cannot be
// forwarded away from the loopback endpoint named in the descriptor.
var noRedirectClient = &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

// Do sends one authenticated request to the endpoint described by d. The
// capability only travels in the Authorization header and is never printed.
func Do(ctx context.Context, d Descriptor, method, path string, body io.Reader) (*http.Response, error) {
	if !isLoopbackURL(d.ControlURL) {
		return nil, errNotLoopback
	}
	request, err := http.NewRequestWithContext(ctx, method, d.ControlURL+path, body)
	if err != nil {
		return nil, err
	}
	requestID, err := NewID()
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+d.Capability)
	request.Header.Set("X-Codex-Bridge-Request-ID", requestID)
	if body != nil {
		request.Header.Set("Content-Type", controlContentType)
	}
	return noRedirectClient.Do(request)
}

// NewID returns 16 random bytes in hex, used for request and message IDs.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
