package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestNotifyClientDoesNotForwardLinkTokenOnRedirect(t *testing.T) {
	client := newNotifyClient()
	calls := 0
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls != 1 {
			t.Fatalf("followed redirect to %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": {"https://other.example/steal"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    req,
		}, nil
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://relay.example/api/x/linked", strings.NewReader(`{"link_token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || calls != 1 {
		t.Fatalf("status %d, requests %d; expected one request and no redirect", resp.StatusCode, calls)
	}
}
