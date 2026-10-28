// Command xpost is a shared X/Twitter crossposting service for swarm relays.
//
// One deployment holds a single X developer app's consumer credentials and
// performs the OAuth 1.0a flow on behalf of any number of relays. Each linked
// Nostr pubkey gets a per-link bearer token; relays present that token to
// POST /v1/post and this service signs and submits the tweet.
//
// Relays configure it via XPOST_SERVICE_URL and store no X secrets — only
// the link token. See ENV_VARIABLES.md for configuration.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hivetalk/swarm/internal/xclient"
)

// Service account record. Persisted to <dbPath>/xpost_accounts.json.
type linkedAccount struct {
	Pubkey        string    `json:"pubkey"`
	ScreenName    string    `json:"screen_name"`
	TwitterUserID string    `json:"twitter_user_id"`
	AccessToken   string    `json:"access_token"`
	AccessSecret  string    `json:"access_secret"`
	LinkToken     string    `json:"link_token"`
	LinkedAt      time.Time `json:"linked_at"`
}

type accountStore struct {
	mu       sync.Mutex
	path     string
	accounts map[string]*linkedAccount // keyed by pubkey
}

func loadAccountStore(path string) (*accountStore, error) {
	s := &accountStore{path: path, accounts: make(map[string]*linkedAccount)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var list []*linkedAccount
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for _, a := range list {
		s.accounts[a.Pubkey] = a
	}
	return s, nil
}

func (s *accountStore) saveLocked() error {
	list := make([]*linkedAccount, 0, len(s.accounts))
	for _, a := range s.accounts {
		list = append(list, a)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *accountStore) Put(a *linkedAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[a.Pubkey] = a
	return s.saveLocked()
}

func (s *accountStore) Delete(pubkey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, pubkey)
	return s.saveLocked()
}

// ByToken resolves the account holding a link bearer token. Returns nil when
// no match — callers treat it as unauthorized.
func (s *accountStore) ByToken(token string) *linkedAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.LinkToken == token {
			return a
		}
	}
	return nil
}

// pendingLink binds an in-flight X OAuth flow to the relay/user that started
// it. Keyed by the oauth request token.
type pendingLink struct {
	Pubkey      string
	Relay       string // relay API origin — receives the /api/x/linked notify
	ReturnTo    string // CMS origin — browser lands here after the flow
	Nonce       string // relay-generated; echoed back so the relay can verify
	TokenSecret string
	ExpiresAt   time.Time
}

type server struct {
	oauth      *xclient.Client
	store      *accountStore
	publicURL  string // externally reachable base, e.g. https://xpost.example.com
	pendingMu  sync.Mutex
	pending    map[string]*pendingLink
	httpClient *http.Client
	// allowedOrigins restricts which origins may start link flows — both the
	// relay notify target and the browser redirect destination must match.
	// Empty means "any public http(s) origin" (warned at startup).
	allowedOrigins map[string]bool
}

func main() {
	publicURL := strings.TrimRight(envOr("XPOST_PUBLIC_URL", ""), "/")
	key := envOr("TWITTER_KEY", "")
	secret := envOr("TWITTER_KEY_SECRET", "")
	if key == "" || secret == "" {
		log.Fatal("TWITTER_KEY and TWITTER_KEY_SECRET are required")
	}
	if publicURL == "" {
		log.Fatal("XPOST_PUBLIC_URL is required (e.g. https://xpost.example.com)")
	}
	dbPath := envOr("XPOST_DB_PATH", "./xpost-data")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		log.Fatalf("creating db path: %v", err)
	}

	store, err := loadAccountStore(filepath.Join(dbPath, "xpost_accounts.json"))
	if err != nil {
		log.Fatalf("loading accounts: %v", err)
	}

	allowed := map[string]bool{}
	for _, o := range strings.Split(envOr("XPOST_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host == "" {
				log.Fatalf("bad XPOST_ALLOWED_ORIGINS entry: %q", o)
			}
			allowed[u.Scheme+"://"+u.Host] = true
		}
	}
	if len(allowed) == 0 {
		log.Println("WARNING: XPOST_ALLOWED_ORIGINS not set — link flows accept any public origin; set it to your relay + CMS origins")
	}

	s := &server{
		oauth:          xclient.New(key, secret),
		store:          store,
		publicURL:      publicURL,
		pending:        make(map[string]*pendingLink),
		httpClient:     newNotifyClient(),
		allowedOrigins: allowed,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/link/start", s.handleLinkStart)
	mux.HandleFunc("/v1/link/callback", s.handleLinkCallback)
	mux.HandleFunc("/v1/post", s.handlePost)
	mux.HandleFunc("/v1/unlink", s.handleUnlink)

	addr := ":" + envOr("PORT", "8090")
	log.Printf("xpost service listening on %s (public: %s)", addr, publicURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func newNotifyClient() *http.Client {
	client := xclient.PublicHTTPClient(15 * time.Second)
	// A 307/308 would forward the link token in the POST body to another host.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func isHTTPOrigin(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// originAllowed reports whether raw may be used as a link-flow relay (notify
// target) or return_to (browser redirect). With XPOST_ALLOWED_ORIGINS set it
// must match a listed origin exactly; otherwise it must be a public http(s)
// URL — which still blocks SSRF to internal hosts but leaves the redirect
// destination unconstrained, so configure the allowlist in production.
func (s *server) originAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if len(s.allowedOrigins) > 0 {
		return s.allowedOrigins[u.Scheme+"://"+u.Host]
	}
	return xclient.IsPublicHTTPURL(raw)
}

// POST /v1/link/start — called by a swarm relay to begin linking one of its
// users. Body: {pubkey, relay, return_to, nonce}. Returns {authorize_url}.
func (s *server) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Pubkey   string `json:"pubkey"`
		Relay    string `json:"relay"`
		ReturnTo string `json:"return_to"`
		Nonce    string `json:"nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Pubkey == "" || req.Nonce == "" ||
		!isHTTPOrigin(req.Relay) || !isHTTPOrigin(req.ReturnTo) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if !s.originAllowed(req.Relay) || !s.originAllowed(req.ReturnTo) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
		return
	}

	token, tokenSecret, err := s.oauth.RequestToken(r.Context(), s.publicURL+"/v1/link/callback")
	if err != nil {
		log.Printf("request_token failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to start X authorization"})
		return
	}

	s.pendingMu.Lock()
	now := time.Now()
	for k, p := range s.pending {
		if now.After(p.ExpiresAt) {
			delete(s.pending, k)
		}
	}
	s.pending[token] = &pendingLink{
		Pubkey:      req.Pubkey,
		Relay:       strings.TrimRight(req.Relay, "/"),
		ReturnTo:    strings.TrimRight(req.ReturnTo, "/"),
		Nonce:       req.Nonce,
		TokenSecret: tokenSecret,
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	s.pendingMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{
		"authorize_url": xclient.AuthorizeURL + "?oauth_token=" + url.QueryEscape(token),
	})
}

// GET /v1/link/callback — X redirects the user's browser here. Exchanges the
// verifier, issues a link token, notifies the originating relay
// (POST {relay}/api/x/linked), then redirects the browser back to the CMS.
func (s *server) handleLinkCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	token, verifier, denied := q.Get("oauth_token"), q.Get("oauth_verifier"), q.Get("denied")

	s.pendingMu.Lock()
	pending, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	s.pendingMu.Unlock()

	dest := s.publicURL
	if ok {
		dest = pending.ReturnTo
	}
	redirect := func(code string) {
		http.Redirect(w, r, dest+"/admin/crosspost?"+code, http.StatusFound)
	}

	if denied != "" || token == "" {
		redirect("x_denied=1")
		return
	}
	if !ok || time.Now().After(pending.ExpiresAt) {
		redirect("x_error=expired")
		return
	}

	accessToken, accessSecret, userID, screenName, err := s.oauth.AccessToken(r.Context(), token, pending.TokenSecret, verifier)
	if err != nil {
		log.Printf("access_token exchange failed for %s: %v", pending.Pubkey, err)
		redirect("x_error=exchange")
		return
	}

	linkBytes := make([]byte, 32)
	if _, err := rand.Read(linkBytes); err != nil {
		redirect("x_error=internal")
		return
	}
	linkToken := hex.EncodeToString(linkBytes)

	acct := &linkedAccount{
		Pubkey:        pending.Pubkey,
		ScreenName:    screenName,
		TwitterUserID: userID,
		AccessToken:   accessToken,
		AccessSecret:  accessSecret,
		LinkToken:     linkToken,
		LinkedAt:      time.Now(),
	}
	if err := s.store.Put(acct); err != nil {
		log.Printf("failed to store account for %s: %v", pending.Pubkey, err)
		redirect("x_error=store")
		return
	}

	// Notify the relay — it validates the nonce against its own pending map
	// and records the link token for this pubkey.
	notify, _ := json.Marshal(map[string]string{
		"nonce":           pending.Nonce,
		"pubkey":          pending.Pubkey,
		"screen_name":     screenName,
		"twitter_user_id": userID,
		"link_token":      linkToken,
	})
	if !xclient.IsPublicHTTPURL(pending.Relay) {
		log.Printf("relay notify target blocked for %s: %s", pending.Pubkey, pending.Relay)
		redirect("x_error=notify")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", pending.Relay+"/api/x/linked", bytes.NewReader(notify))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			log.Printf("relay notify failed for %s (%s): %v", pending.Pubkey, pending.Relay, err)
			redirect("x_error=notify")
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("relay notify rejected for %s: %s", pending.Pubkey, resp.Status)
			redirect("x_error=notify")
			return
		}
	}

	log.Printf("linked @%s for pubkey %s", screenName, pending.Pubkey)
	redirect("x_linked=1")
}

// POST /v1/post — relay submits cleaned tweet text for a linked account.
// Auth: Bearer <link_token>. Body: {pubkey, event_id, text, text_media?,
// media_url?}. When media_url is a video the service uploads it as an X media
// attachment and posts text_media; on failure it posts text with the URL in.
// Returns {tweet_id}.
func (s *server) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	acct := s.store.ByToken(bearerToken(r))
	if acct == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown link token"})
		return
	}
	var req struct {
		Pubkey    string `json:"pubkey"`
		EventID   string `json:"event_id"`
		Text      string `json:"text"`
		TextMedia string `json:"text_media"`
		MediaURL  string `json:"media_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Pubkey != "" && req.Pubkey != acct.Pubkey {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "pubkey mismatch"})
		return
	}
	if req.MediaURL != "" && !xclient.IsPublicHTTPURL(req.MediaURL) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid media_url"})
		return
	}

	text := req.Text
	var mediaIDs []string
	if req.MediaURL != "" {
		mediaID, err := s.oauth.UploadMedia(r.Context(), acct.AccessToken, acct.AccessSecret, req.MediaURL)
		if err != nil {
			log.Printf("media upload failed for %s (@%s), posting text only: %v", acct.Pubkey, acct.ScreenName, err)
		} else {
			mediaIDs = []string{mediaID}
			if req.TextMedia != "" {
				text = req.TextMedia
			}
		}
	}

	tweetID, err := s.oauth.PostTweetWithMedia(r.Context(), acct.AccessToken, acct.AccessSecret, text, mediaIDs)
	if err != nil {
		log.Printf("tweet failed for %s (@%s): %v", acct.Pubkey, acct.ScreenName, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tweet_id": tweetID})
}

// POST /v1/unlink — Bearer <link_token>, body {pubkey}. Removes the account.
func (s *server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	acct := s.store.ByToken(bearerToken(r))
	if acct == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown link token"})
		return
	}
	var req struct {
		Pubkey string `json:"pubkey"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Pubkey != "" && req.Pubkey != acct.Pubkey {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "pubkey mismatch"})
		return
	}
	if err := s.store.Delete(acct.Pubkey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to unlink"})
		return
	}
	log.Printf("unlinked @%s for pubkey %s", acct.ScreenName, acct.Pubkey)
	w.WriteHeader(http.StatusOK)
}
