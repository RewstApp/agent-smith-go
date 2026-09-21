package utils

import (
	"context"
	"io"
	"net/http"

	"github.com/RewstApp/agent-smith-go/internal/version"
)

func NewRequestWithContext(
	ctx context.Context,
	method string,
	url string,
	body io.Reader,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return req, err
	}

	// The header has always carried the bare number ("1.5.7"); the device twin
	// and the startup log carry the tag form ("v1.5.7") — see version.Version.
	req.Header.Set("x-rewst-agent-smith-version", version.Number())

	return req, nil
}

func NewRequest(method string, url string, body io.Reader) (*http.Request, error) {
	return NewRequestWithContext(context.Background(), method, url, body)
}
