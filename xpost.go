package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hivetalk/swarm/internal/xclient"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// Crossposter tweets kind-1 notes authored by linked team members to their
// connected X accounts. It hooks relay.OnEventSaved so it covers both
// websocket-published notes and scheduler-published notes (relay.AddEvent
// fires OnEventSaved after storing).
//
// Two posting modes:
//   - local: TWITTER_KEY/TWITTER_KEY_SECRET set — this relay runs the OAuth
//     flow itself and signs tweets directly.
//   - delegate: XPOST_SERVICE_URL set — a shared xpost service runs the OAuth
//     flow and posts tweets; this relay keeps only a per-link bearer token.
//
// Matching rules (all must hold):
//   - event kind is 1
//   - author has a linked X account with Enabled=true
//   - event.created_at >= account.LinkedAt (never backfire old/imported notes)
//   - event has no "e" tags (replies/quotes-via-e don't crosspost; NIP-18 "q"
//     tag quotes do)
//   - event ID not already posted (dedupe)
type Crossposter struct {
	store      *XAccountStore
	oauth      *xclient.Client
	serviceURL string // delegate service base URL, "" when in local mode
	enabled    bool   // false when neither mode is configured
	relayURL   string // e.g. "wss://example.com" — included as nevent relay hint

	pendingMu sync.Mutex
	pending   map[string]*xPendingAuth // oauth request token → pending link (local mode)

	linkPendingMu sync.Mutex
	linkPending   map[string]*xLinkPending // nonce → pending delegate link
}

type xPendingAuth struct {
	Pubkey      string
	TokenSecret string
	ReturnTo    string // CMS origin to redirect to after X callback
	ExpiresAt   time.Time
}

// xLinkPending binds a delegate-mode link notification (/api/x/linked) to the
// auth-start that produced it, so a forged notification can't attach a token
// to an arbitrary pubkey.
type xLinkPending struct {
	Pubkey    string
	ExpiresAt time.Time
}

func NewCrossposter(store *XAccountStore, consumerKey, consumerSecret, serviceURL, relayWSURL string) *Crossposter {
	serviceURL = strings.TrimRight(serviceURL, "/")
	return &Crossposter{
		store:       store,
		oauth:       xclient.New(consumerKey, consumerSecret),
		serviceURL:  serviceURL,
		enabled:     (consumerKey != "" && consumerSecret != "") || serviceURL != "",
		relayURL:    relayWSURL,
		pending:     make(map[string]*xPendingAuth),
		linkPending: make(map[string]*xLinkPending),
	}
}

// ---------------------------------------------------------------------------
// Event processing
// ---------------------------------------------------------------------------

// OnEventSaved is registered on relay.OnEventSaved. It runs in the khatru
// publish path, so the actual X call happens in a goroutine.
func (c *Crossposter) OnEventSaved(ctx context.Context, event *nostr.Event) {
	if !c.enabled || event == nil || event.Kind != 1 {
		return
	}

	acct := c.store.Get(event.PubKey)
	if acct == nil || !acct.Enabled {
		return
	}

	// Replies don't crosspost — a tweet without its thread context is noise.
	// Any "e" tag (NIP-10 root/reply/mention marker or positional) counts.
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "e" {
			return
		}
	}

	// Don't backfire: only notes authored after the account was linked.
	createdAt := time.Unix(int64(event.CreatedAt), 0)
	if createdAt.Before(acct.LinkedAt) {
		return
	}

	if acct.hasPosted(event.ID) {
		return
	}

	go c.crosspost(event)
}

func (c *Crossposter) crosspost(event *nostr.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	acct := c.store.Get(event.PubKey)
	if acct == nil || !acct.Enabled || acct.hasPosted(event.ID) {
		return
	}

	text := buildTweetText(event, c.relayURL)

	var tweetID string
	var err error
	if acct.Mode == "delegate" {
		tweetID, err = xclient.PostViaService(ctx, c.serviceURL, acct.LinkToken, acct.UserPubkey, event.ID, text)
	} else {
		tweetID, err = c.oauth.PostTweet(ctx, acct.AccessToken, acct.AccessSecret, text)
	}

	now := time.Now()
	if err != nil {
		logWithFields("error", "X crosspost failed", map[string]interface{}{
			"event_id": event.ID,
			"pubkey":   event.PubKey,
			"error":    err.Error(),
		})
		c.store.Mutate(event.PubKey, func(a *XAccount) {
			a.LastError = err.Error()
		})
		return
	}

	logWithFields("info", "Crossposted note to X", map[string]interface{}{
		"event_id": event.ID,
		"pubkey":   event.PubKey,
		"tweet_id": tweetID,
	})
	c.store.Mutate(event.PubKey, func(a *XAccount) {
		a.markPosted(event.ID, now)
	})
}

// ---------------------------------------------------------------------------
// Tweet text
// ---------------------------------------------------------------------------

// nostr: URI references inside note content. Bech32 charset excludes 1, b, i,
// o after the HRP separator (the '1' here is part of the HRP, e.g. "npub1").
var nostrURIRe = regexp.MustCompile(`nostr:(npub1|nprofile1|note1|nevent1|naddr1)[023456789acdefghjklmnpqrstuvwxyz]+`)

// nsec matches its whole token liberally — even a malformed one must be fully
// consumed so no part of a private key leaks into a tweet.
var nostrNsecRe = regexp.MustCompile(`nostr:nsec1[a-z0-9]+`)

// http(s) URLs each count as t.co length on X regardless of actual length.
var httpURLRe = regexp.MustCompile(`https?://[^\s]+`)

const (
	xMaxWeightedLen = 280
	xURLLen         = 23 // t.co wrapped length
)

// cleanNostrLinks turns nostr:npub1… / nostr:note1… etc. references into
// readable https://nostr.at/<bech32> links. nsec tokens are redacted — a note
// that somehow contains a private key must not have it amplified to X.
func cleanNostrLinks(content string) string {
	content = nostrNsecRe.ReplaceAllString(content, "[redacted]")
	return nostrURIRe.ReplaceAllStringFunc(content, func(m string) string {
		return "https://nostr.at/" + strings.TrimPrefix(m, "nostr:")
	})
}

// weightedTweetLen approximates X's weighted character count: every URL token
// counts 23, everything else counts one per rune. (CJK weighted 2 and
// punctuation normalization are ignored — good enough for truncation budget.)
func weightedTweetLen(text string) int {
	length := 0
	last := 0
	for _, loc := range httpURLRe.FindAllStringIndex(text, -1) {
		length += utf8.RuneCountInString(text[last:loc[0]])
		length += xURLLen
		last = loc[1]
	}
	length += utf8.RuneCountInString(text[last:])
	return length
}

// buildTweetText produces the tweet body: cleaned note text, then a link back
// to the original note on nostr.at. If the cleaned text is too long it is
// truncated at a word boundary with an ellipsis so the footer link always fits.
func buildTweetText(event *nostr.Event, relayWSURL string) string {
	text := strings.TrimSpace(cleanNostrLinks(event.Content))

	sourceLink := nostrATEventURL(event, relayWSURL)
	// Footer is "\n\n" + link; the link counts as one URL.
	budget := xMaxWeightedLen - 2 - xURLLen

	if weightedTweetLen(text) > budget {
		text = truncateToWeighted(text, budget-1) + "…" // ellipsis rune
	}

	return text + "\n\n" + sourceLink
}

// nostrATEventURL builds https://nostr.at/nevent1… for the source event,
// embedding the author and this relay as hints.
func nostrATEventURL(event *nostr.Event, relayWSURL string) string {
	relays := []string{}
	if relayWSURL != "" {
		relays = []string{relayWSURL}
	}
	nevent, err := nip19.EncodeEvent(event.ID, relays, event.PubKey)
	if err != nil {
		return "https://nostr.at/" + event.ID
	}
	return "https://nostr.at/" + nevent
}

// truncateToWeighted cuts s so its weighted length fits within budget,
// preferring a word boundary. Returned text has no trailing ellipsis — the
// caller appends it.
func truncateToWeighted(s string, budget int) string {
	if weightedTweetLen(s) <= budget {
		return s
	}
	if budget <= 0 {
		return ""
	}

	// Split into alternating text/URL segments so URLs are charged once (23)
	// instead of per rune.
	type seg struct {
		text  string
		isURL bool
	}
	var segs []seg
	last := 0
	for _, loc := range httpURLRe.FindAllStringIndex(s, -1) {
		if loc[0] > last {
			segs = append(segs, seg{s[last:loc[0]], false})
		}
		segs = append(segs, seg{s[loc[0]:loc[1]], true})
		last = loc[1]
	}
	if last < len(s) {
		segs = append(segs, seg{s[last:], false})
	}

	cost := 0
	var b strings.Builder
outer:
	for _, sg := range segs {
		if sg.isURL {
			if cost+xURLLen > budget {
				break
			}
			b.WriteString(sg.text)
			cost += xURLLen
			continue
		}
		for _, r := range sg.text {
			if cost+1 > budget {
				break outer
			}
			b.WriteRune(r)
			cost++
		}
	}

	out := b.String()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	if len(out) < len(s) {
		// We stopped because the next char would exceed the budget. If out
		// ends inside a word (non-space char) and s continues the word
		// (non-space char), drop the trailing partial word. If either side is
		// a boundary — or out ends on a complete URL — keep it whole.
		lastIsSpace := out[len(out)-1] == ' ' || out[len(out)-1] == '\n' || out[len(out)-1] == '\t'
		next := s[len(out)]
		nextIsSpace := next == ' ' || next == '\n' || next == '\t'
		if !lastIsSpace && !nextIsSpace {
			if idx := strings.LastIndexAny(out, " \n\t"); idx > 0 {
				out = out[:idx]
			}
		}
	}
	return strings.TrimRight(out, " \n\t")
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (c *Crossposter) enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// configured reports whether crossposting can run — either local X app
// credentials or a delegate service URL is set.
func (c *Crossposter) configured() bool { return c.enabled }

// delegateMode reports whether links/posts route through the shared service.
func (c *Crossposter) delegateMode() bool { return c.serviceURL != "" }

// requestOrigin returns the CMS origin for post-callback redirect. Prefer the
// explicit return_to passed by the frontend (validated to http/https scheme);
// fall back to the Origin header, then this host.
func requestOrigin(r *http.Request, explicit string) string {
	isHTTPURL := func(s string) bool {
		u, err := url.Parse(s)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	}
	if isHTTPURL(explicit) {
		u, _ := url.Parse(explicit)
		return u.Scheme + "://" + u.Host
	}
	if origin := r.Header.Get("Origin"); isHTTPURL(origin) {
		u, _ := url.Parse(origin)
		return u.Scheme + "://" + u.Host
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// callbackURL derives the X oauth_callback for this deployment from the
// inbound request (respects X-Forwarded-* for proxied setups).
func (c *Crossposter) callbackURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host + "/api/x/auth/callback"
}

// HandleStatus: GET /api/x/status — NIP-98 authed, per-user.
func (c *Crossposter) HandleStatus(w http.ResponseWriter, r *http.Request) {
	c.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := map[string]interface{}{
		"configured": c.configured(),
		"linked":     false,
	}
	if acct := c.store.Get(pubkey); acct != nil {
		resp["linked"] = true
		resp["enabled"] = acct.Enabled
		resp["screen_name"] = acct.ScreenName
		resp["twitter_user_id"] = acct.TwitterUserID
		resp["linked_at"] = acct.LinkedAt
		if acct.LastPostedAt != nil {
			resp["last_posted_at"] = acct.LastPostedAt
		}
		if acct.LastError != "" {
			resp["last_error"] = acct.LastError
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleAuthStart: POST /api/x/auth/start — NIP-98 authed. Body:
// {"return_to": "https://cms.example.com"}. Returns {"authorize_url": "..."}.
// In delegate mode the request is forwarded to the shared service, which
// issues the X authorize URL directly.
func (c *Crossposter) HandleAuthStart(w http.ResponseWriter, r *http.Request) {
	c.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if !c.configured() {
		http.Error(w, "X crossposting is not configured on this server", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ReturnTo string `json:"return_to"`
	}
	// Body optional — tolerate empty.
	_ = json.NewDecoder(r.Body).Decode(&req)

	if c.delegateMode() {
		c.handleAuthStartDelegate(w, r, pubkey, req.ReturnTo)
		return
	}
	c.handleAuthStartLocal(w, r, pubkey, req.ReturnTo)
}

// handleAuthStartLocal runs the 3-legged flow against X directly (local mode).
func (c *Crossposter) handleAuthStartLocal(w http.ResponseWriter, r *http.Request, pubkey, returnTo string) {
	token, secret, err := c.oauth.RequestToken(r.Context(), c.callbackURL(r))
	if err != nil {
		logWithFields("error", "X request_token failed", map[string]interface{}{
			"pubkey": pubkey,
			"error":  err.Error(),
		})
		http.Error(w, "Failed to start X authorization", http.StatusBadGateway)
		return
	}

	c.pendingMu.Lock()
	now := sweepPendings(c.pending)
	c.pending[token] = &xPendingAuth{
		Pubkey:      pubkey,
		TokenSecret: secret,
		ReturnTo:    requestOrigin(r, returnTo),
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	c.pendingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"authorize_url": xclient.AuthorizeURL + "?oauth_token=" + url.QueryEscape(token),
	})
}

// sweepPendings drops expired entries and returns the time used for the check.
func sweepPendings(pending map[string]*xPendingAuth) time.Time {
	now := time.Now()
	for k, p := range pending {
		if now.After(p.ExpiresAt) {
			delete(pending, k)
		}
	}
	return now
}

// handleAuthStartDelegate asks the shared service to start a link flow. It
// passes a nonce that the service echoes in its /api/x/linked notification,
// binding the link_token delivery to this relay-initiated flow.
func (c *Crossposter) handleAuthStartDelegate(w http.ResponseWriter, r *http.Request, pubkey, returnTo string) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	nonce := hex.EncodeToString(nonceBytes)

	payload, _ := json.Marshal(map[string]string{
		"pubkey":    pubkey,
		"relay":     requestOrigin(r, ""),
		"return_to": requestOrigin(r, returnTo),
		"nonce":     nonce,
	})

	reqCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	sreq, err := http.NewRequestWithContext(reqCtx, "POST", c.serviceURL+"/v1/link/start", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	sreq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(sreq)
	if err != nil {
		logWithFields("error", "delegate link/start unreachable", map[string]interface{}{
			"pubkey": pubkey,
			"error":  err.Error(),
		})
		http.Error(w, "Crossposting service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var svcResp struct {
		AuthorizeURL string `json:"authorize_url"`
		Error        string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&svcResp)
	if resp.StatusCode != http.StatusOK || svcResp.AuthorizeURL == "" {
		logWithFields("error", "delegate link/start failed", map[string]interface{}{
			"pubkey": pubkey,
			"status": resp.Status,
			"detail": svcResp.Error,
		})
		http.Error(w, "Failed to start X authorization", http.StatusBadGateway)
		return
	}

	c.linkPendingMu.Lock()
	c.linkPending[nonce] = &xLinkPending{Pubkey: pubkey, ExpiresAt: time.Now().Add(10 * time.Minute)}
	c.linkPendingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"authorize_url": svcResp.AuthorizeURL})
}

// HandleLinked: POST /api/x/linked — called by the delegate service after a
// user completes X authorization. Delivers the per-link bearer token.
func (c *Crossposter) HandleLinked(w http.ResponseWriter, r *http.Request) {
	c.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.delegateMode() {
		http.Error(w, "Not in delegate mode", http.StatusBadRequest)
		return
	}

	var req struct {
		Nonce         string `json:"nonce"`
		Pubkey        string `json:"pubkey"`
		ScreenName    string `json:"screen_name"`
		TwitterUserID string `json:"twitter_user_id"`
		LinkToken     string `json:"link_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Nonce == "" || req.LinkToken == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	c.linkPendingMu.Lock()
	pending, ok := c.linkPending[req.Nonce]
	if ok {
		delete(c.linkPending, req.Nonce)
	}
	// Sweep expired while holding the lock.
	now := time.Now()
	for k, p := range c.linkPending {
		if now.After(p.ExpiresAt) {
			delete(c.linkPending, k)
		}
	}
	c.linkPendingMu.Unlock()

	if !ok || pending.Pubkey != req.Pubkey || now.After(pending.ExpiresAt) {
		logWithFields("warn", "linked notification with unknown/stale nonce", map[string]interface{}{
			"pubkey": req.Pubkey,
		})
		http.Error(w, "Unknown or expired link", http.StatusForbidden)
		return
	}

	acct := &XAccount{
		UserPubkey:    req.Pubkey,
		Mode:          "delegate",
		TwitterUserID: req.TwitterUserID,
		ScreenName:    req.ScreenName,
		LinkToken:     req.LinkToken,
		Enabled:       true,
		LinkedAt:      now,
	}
	if prev := c.store.Get(req.Pubkey); prev != nil {
		acct.PostedIDs = prev.PostedIDs
		acct.LastPostedAt = prev.LastPostedAt
		if prev.TwitterUserID != req.TwitterUserID {
			acct.PostedIDs = nil
			acct.LastPostedAt = nil
		}
	}
	if err := c.store.Put(acct); err != nil {
		http.Error(w, "Failed to save link", http.StatusInternalServerError)
		return
	}

	logWithFields("info", "X account linked (delegate)", map[string]interface{}{
		"pubkey":      req.Pubkey,
		"screen_name": req.ScreenName,
	})
	w.WriteHeader(http.StatusOK)
}

// HandleAuthCallback: GET /api/x/auth/callback — public; X redirects the
// user's browser here with oauth_token + oauth_verifier (+ denied).
func (c *Crossposter) HandleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	token := q.Get("oauth_token")
	verifier := q.Get("oauth_verifier")
	denied := q.Get("denied")

	c.pendingMu.Lock()
	pending, ok := c.pending[token]
	if ok {
		delete(c.pending, token)
	}
	c.pendingMu.Unlock()

	// If the pending record is gone (expired, restart, or bad token), we can't
	// know where to redirect — land on the admin crosspost path on this host.
	fallback := requestOrigin(r, "") + "/admin/crosspost"
	if denied != "" || token == "" {
		http.Redirect(w, r, fallback+"?x_denied=1", http.StatusFound)
		return
	}
	if !ok || time.Now().After(pending.ExpiresAt) {
		http.Redirect(w, r, fallback+"?x_error=expired", http.StatusFound)
		return
	}

	returnTo := pending.ReturnTo + "/admin/crosspost"
	if !c.configured() {
		http.Redirect(w, r, returnTo+"?x_error=unconfigured", http.StatusFound)
		return
	}

	accessToken, accessSecret, userID, screenName, err := c.oauth.AccessToken(r.Context(), token, pending.TokenSecret, verifier)
	if err != nil {
		logWithFields("error", "X access_token exchange failed", map[string]interface{}{
			"pubkey": pending.Pubkey,
			"error":  err.Error(),
		})
		http.Redirect(w, r, returnTo+"?x_error=exchange", http.StatusFound)
		return
	}

	acct := &XAccount{
		UserPubkey:    pending.Pubkey,
		Mode:          "local",
		TwitterUserID: userID,
		ScreenName:    screenName,
		AccessToken:   accessToken,
		AccessSecret:  accessSecret,
		Enabled:       true,
		LinkedAt:      time.Now(),
	}
	// Preserve dedupe history if the account was previously linked.
	if prev := c.store.Get(pending.Pubkey); prev != nil {
		acct.PostedIDs = prev.PostedIDs
		acct.LastPostedAt = prev.LastPostedAt
		// If the X identity changed, drop the old dedupe list — it belongs to
		// the other account.
		if prev.TwitterUserID != userID {
			acct.PostedIDs = nil
			acct.LastPostedAt = nil
		}
	}
	if err := c.store.Put(acct); err != nil {
		logWithFields("error", "Failed to save X account", map[string]interface{}{
			"pubkey": pending.Pubkey,
			"error":  err.Error(),
		})
		http.Redirect(w, r, returnTo+"?x_error=store", http.StatusFound)
		return
	}

	logWithFields("info", "X account linked", map[string]interface{}{
		"pubkey":      pending.Pubkey,
		"screen_name": screenName,
	})
	http.Redirect(w, r, returnTo+"?x_linked=1", http.StatusFound)
}

// HandleDisconnect: POST /api/x/disconnect — NIP-98 authed.
func (c *Crossposter) HandleDisconnect(w http.ResponseWriter, r *http.Request) {
	c.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Delegate accounts also exist on the shared service — ask it to drop the
	// X tokens (best-effort; local link is removed regardless).
	if acct := c.store.Get(pubkey); acct != nil && acct.Mode == "delegate" && c.delegateMode() {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]string{"pubkey": pubkey})
		if req, err := http.NewRequestWithContext(ctx, "POST", c.serviceURL+"/v1/unlink", bytes.NewReader(payload)); err == nil {
			req.Header.Set("Authorization", "Bearer "+acct.LinkToken)
			req.Header.Set("Content-Type", "application/json")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}

	if err := c.store.Delete(pubkey); err != nil {
		http.Error(w, "Failed to disconnect", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// HandleToggle: POST /api/x/toggle — NIP-98 authed. Body: {"enabled": bool}.
func (c *Crossposter) HandleToggle(w http.ResponseWriter, r *http.Request) {
	c.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if err := c.store.Mutate(pubkey, func(a *XAccount) { a.Enabled = req.Enabled }); err != nil {
		http.Error(w, "No linked X account", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}
