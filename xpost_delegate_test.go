package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hivetalk/swarm/internal/xclient"
	"github.com/nbd-wtf/go-nostr"
)

// testStore returns an XAccountStore backed by a temp dir.
func testStore(t *testing.T) *XAccountStore {
	t.Helper()
	store, err := NewXAccountStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewXAccountStore: %v", err)
	}
	return store
}

// nip98Auth builds a signed NIP-98 Authorization header for a request.
// The pubkey is registered in the global allowed-names map.
func nip98Auth(t *testing.T, method, fullURL string) (header, pubkey string) {
	t.Helper()
	sk := nostr.GeneratePrivateKey()
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if data.Names == nil {
		data.Names = map[string]string{}
	}
	data.Names["delegate-test-"+pk[:8]] = pk

	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      27235,
		Tags:      nostr.Tags{{"u", fullURL}, {"method", method}},
	}
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("sign NIP-98 event: %v", err)
	}
	raw, _ := json.Marshal(ev)
	return "Nostr " + base64.StdEncoding.EncodeToString(raw), pk
}

func TestDelegateModeConfig(t *testing.T) {
	c := NewCrossposter(testStore(t), "", "", "https://xpost.example.com/", "wss://relay.example.com", nil)
	if !c.configured() {
		t.Fatal("crossposter with service URL should be configured")
	}
	if !c.delegateMode() {
		t.Fatal("expected delegate mode")
	}
	if c.serviceURL != "https://xpost.example.com" {
		t.Fatalf("service URL should have trailing slash trimmed, got %q", c.serviceURL)
	}

	local := NewCrossposter(testStore(t), "k", "s", "", "", nil)
	if !local.configured() || local.delegateMode() {
		t.Fatal("local credentials should give configured non-delegate mode")
	}

	none := NewCrossposter(testStore(t), "", "", "", "", nil)
	if none.configured() {
		t.Fatal("no config should mean unconfigured")
	}
}

func TestHandleLinkedNonceBinding(t *testing.T) {
	store := testStore(t)
	c := NewCrossposter(store, "", "", "https://xpost.example.com", "", nil)

	pubkey := "00000000000000000000000000000000000000000000000000000000000000aa"
	c.linkPending["nonce-good"] = &xLinkPending{Pubkey: pubkey, ExpiresAt: time.Now().Add(time.Minute)}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/x/linked", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c.HandleLinked(rec, req)
		return rec
	}

	// Unknown nonce → 403, nothing stored.
	rec := post(`{"nonce":"bogus","pubkey":"` + pubkey + `","link_token":"tok"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unknown nonce should be 403, got %d", rec.Code)
	}
	if store.Get(pubkey) != nil {
		t.Fatal("no account should be stored for an unknown nonce")
	}

	// Nonce bound to a different pubkey → 403.
	c.linkPending["nonce-other"] = &xLinkPending{Pubkey: "ff" + pubkey[2:], ExpiresAt: time.Now().Add(time.Minute)}
	rec = post(`{"nonce":"nonce-other","pubkey":"` + pubkey + `","link_token":"tok"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pubkey mismatch should be 403, got %d", rec.Code)
	}

	// Valid nonce+pubkey → 200, account stored in delegate mode.
	rec = post(`{"nonce":"nonce-good","pubkey":"` + pubkey + `","screen_name":"alice","twitter_user_id":"42","link_token":"tok123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid link should be 200, got %d", rec.Code)
	}
	acct := store.Get(pubkey)
	if acct == nil {
		t.Fatal("account not stored")
	}
	if acct.Mode != "delegate" || acct.LinkToken != "tok123" || acct.ScreenName != "alice" || !acct.Enabled {
		t.Fatalf("bad stored account: %+v", acct)
	}

	// Nonce is single-use — replay must be rejected.
	rec = post(`{"nonce":"nonce-good","pubkey":"` + pubkey + `","link_token":"tok999"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("replayed nonce should be 403, got %d", rec.Code)
	}
	if store.Get(pubkey).LinkToken != "tok123" {
		t.Fatal("replayed nonce must not overwrite the link token")
	}

	// Expired pending → 403.
	c.linkPending["nonce-old"] = &xLinkPending{Pubkey: pubkey, ExpiresAt: time.Now().Add(-time.Minute)}
	rec = post(`{"nonce":"nonce-old","pubkey":"` + pubkey + `","link_token":"tok"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expired nonce should be 403, got %d", rec.Code)
	}

	// Local-mode crossposter rejects the endpoint entirely.
	local := NewCrossposter(testStore(t), "k", "s", "", "", nil)
	req := httptest.NewRequest("POST", "/api/x/linked", strings.NewReader(`{"nonce":"x","link_token":"t"}`))
	rec = httptest.NewRecorder()
	local.HandleLinked(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-delegate /linked should be 400, got %d", rec.Code)
	}
}

func TestDelegateAuthStart(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  map[string]string
		gotCalls int
	)
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/link/start" {
			t.Errorf("unexpected service path %s", r.URL.Path)
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		mu.Lock()
		gotCalls++
		json.NewDecoder(r.Body).Decode(&gotBody)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"authorize_url": "https://api.twitter.com/oauth/authorize?oauth_token=xyz"})
	}))
	defer svc.Close()

	c := NewCrossposter(testStore(t), "", "", svc.URL, "", nil)

	url := "http://relay.example.com/api/x/auth/start"
	auth, pk := nip98Auth(t, "POST", url)

	req := httptest.NewRequest("POST", url, strings.NewReader(`{"return_to":"https://cms.example.com"}`))
	req.Host = "relay.example.com"
	req.Header.Set("Authorization", auth)
	rec := httptest.NewRecorder()
	c.HandleAuthStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("auth start should be 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.AuthorizeURL == "" {
		t.Fatalf("missing authorize_url in response: %s", rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("service should have been called once, got %d", gotCalls)
	}
	if gotBody["pubkey"] != pk {
		t.Fatalf("service got wrong pubkey: %q", gotBody["pubkey"])
	}
	if gotBody["nonce"] == "" {
		t.Fatal("service received no nonce")
	}
	if gotBody["relay"] != "http://relay.example.com" {
		t.Fatalf("service got wrong relay: %q", gotBody["relay"])
	}
	if gotBody["return_to"] != "https://cms.example.com" {
		t.Fatalf("service got wrong return_to: %q", gotBody["return_to"])
	}

	// The nonce must be tracked pending so a later /api/x/linked can verify.
	if _, ok := c.linkPending[gotBody["nonce"]]; !ok {
		t.Fatal("nonce not recorded in linkPending")
	}
}

func TestCrosspostDelegatesToService(t *testing.T) {
	var (
		gotAuth string
		gotBody map[string]string
		calls   int
		mu      sync.Mutex
	)
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/post" {
			calls++
			gotAuth = r.Header.Get("Authorization")
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"tweet_id": "tweet-1"})
			return
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer svc.Close()

	store := testStore(t)
	pubkey := "00000000000000000000000000000000000000000000000000000000000000bb"
	if err := store.Put(&XAccount{
		UserPubkey:    pubkey,
		Mode:          "delegate",
		ScreenName:    "alice",
		TwitterUserID: "42",
		LinkToken:     "secret-link-token",
		Enabled:       true,
		LinkedAt:      time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	c := NewCrossposter(store, "", "", svc.URL, "wss://relay.example.com", nil)

	event := &nostr.Event{
		ID:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PubKey:    pubkey,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   "hello from nostr",
		Tags:      nostr.Tags{},
	}
	c.crosspost(event)

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("service /v1/post should be called once, got %d", calls)
	}
	if gotAuth != "Bearer secret-link-token" {
		t.Fatalf("wrong auth header: %q", gotAuth)
	}
	if gotBody["pubkey"] != pubkey {
		t.Fatalf("wrong pubkey payload: %q", gotBody["pubkey"])
	}
	if gotBody["event_id"] != event.ID {
		t.Fatalf("wrong event_id payload: %q", gotBody["event_id"])
	}
	if !strings.HasPrefix(gotBody["text"], "hello from nostr\n\nhttps://nostr.at/nevent1") {
		t.Fatalf("cleaned text not forwarded: %q", gotBody["text"])
	}

	// Success should mark the event posted for dedupe.
	acct := store.Get(pubkey)
	if acct == nil || !acct.hasPosted(event.ID) || acct.LastPostedAt == nil {
		t.Fatalf("event not marked posted: %+v", acct)
	}
}

func TestCrosspostDelegateErrorRecorded(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	defer svc.Close()

	store := testStore(t)
	pubkey := "00000000000000000000000000000000000000000000000000000000000000cc"
	_ = store.Put(&XAccount{
		UserPubkey: pubkey,
		Mode:       "delegate",
		LinkToken:  "tok",
		Enabled:    true,
		LinkedAt:   time.Now().Add(-time.Hour),
	})

	c := NewCrossposter(store, "", "", svc.URL, "", nil)
	c.crosspost(&nostr.Event{
		ID:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PubKey:    pubkey,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   "will fail",
		Tags:      nostr.Tags{},
	})

	acct := store.Get(pubkey)
	if acct == nil || !strings.Contains(acct.LastError, "upstream exploded") {
		t.Fatalf("service error not recorded on account: %+v", acct)
	}
	if acct.hasPosted("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatal("failed post must not be marked posted")
	}
}

func TestPostViaServiceRejectsBadToken(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "unknown link token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tweet_id":"ok-1"}`)
	}))
	defer svc.Close()

	// Bad token → error propagates up to the caller.
	if _, err := xclient.PostViaService(context.Background(), svc.URL, "bad", "pk", "ev", "hi", "", ""); err == nil {
		t.Fatal("expected error for bad token")
	}
	if id, err := xclient.PostViaService(context.Background(), svc.URL, "good", "pk", "ev", "hi", "", ""); err != nil || id != "ok-1" {
		t.Fatalf("good token should post: id=%q err=%v", id, err)
	}
}
