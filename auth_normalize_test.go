package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testEventJSON = `{"id":"` +
	strings.Repeat("ab", 32) +
	`","pubkey":"` + strings.Repeat("cd", 32) +
	`","sig":"` + strings.Repeat("ef", 64) +
	`","kind":24242,"created_at":1772900000,"tags":[["t","upload"],["expiration","1792900000"]],"content":"Upload Blob"}`

func TestNormalizeNostrAuthToken(t *testing.T) {
	// A token carrying URL-alphabet characters (- and _) must normalize to
	// standard base64 that StdEncoding accepts, with identical decoded bytes.
	want := []byte(testEventJSON)
	urlTok := base64.RawURLEncoding.EncodeToString(want)
	if got, err := base64.StdEncoding.DecodeString(normalizeNostrAuthToken(urlTok)); err != nil || string(got) != string(want) {
		t.Fatalf("url token: err=%v match=%v", err, string(got) == string(want))
	}
	// Force URL-alphabet chars into the token payload so the mapping itself
	// is exercised (not just the padding branch).
	dataWithUrlChars := []byte{251, 255, 190, 0, 1, 250, 255, 191}
	urlTok2 := base64.RawURLEncoding.EncodeToString(dataWithUrlChars)
	if !strings.ContainsAny(urlTok2, "-_") {
		t.Skip("test data did not produce url-alphabet chars")
	}
	if got, err := base64.StdEncoding.DecodeString(normalizeNostrAuthToken(urlTok2)); err != nil || string(got) != string(dataWithUrlChars) {
		t.Fatalf("url-alphabet chars: err=%v match=%v", err, string(got) == string(dataWithUrlChars))
	}
	// Standard tokens pass through untouched.
	stdTok := base64.StdEncoding.EncodeToString(want)
	if normalizeNostrAuthToken(stdTok) != stdTok {
		t.Fatal("std token was modified")
	}
	// URL-safe with padding also normalizes.
	urlPadTok := base64.URLEncoding.EncodeToString(want)
	if got, err := base64.StdEncoding.DecodeString(normalizeNostrAuthToken(urlPadTok)); err != nil || string(got) != string(want) {
		t.Fatalf("url+padded: err=%v match=%v", err, string(got) == string(want))
	}
}

func TestAuthNormalizeMiddleware(t *testing.T) {
	var seenAuth string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
	})
	h := authNormalizeMiddleware(next)

	eventJSON := []byte(testEventJSON)
	urlTok := base64.RawURLEncoding.EncodeToString(eventJSON)

	req := httptest.NewRequest("PUT", "http://relay/upload", nil)
	req.Header.Set("Authorization", "Nostr "+urlTok)
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := "Nostr " + base64.StdEncoding.EncodeToString(eventJSON)
	if seenAuth != want {
		t.Fatalf("url token not normalized: got %q want %q", seenAuth, want)
	}

	// Standard token left untouched.
	req2 := httptest.NewRequest("PUT", "http://relay/upload", nil)
	std := "Nostr " + base64.StdEncoding.EncodeToString(eventJSON)
	req2.Header.Set("Authorization", std)
	h.ServeHTTP(httptest.NewRecorder(), req2)
	if seenAuth != std {
		t.Fatalf("std token modified: got %q", seenAuth)
	}

	// Non-Nostr auth untouched.
	req3 := httptest.NewRequest("GET", "http://relay/", nil)
	req3.Header.Set("Authorization", "Bearer abc")
	h.ServeHTTP(httptest.NewRecorder(), req3)
	if seenAuth != "Bearer abc" {
		t.Fatalf("non-nostr header modified: got %q", seenAuth)
	}
}
