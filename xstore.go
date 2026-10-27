package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// XAccount links a nostr pubkey (team member in nostr.json) to an X/Twitter
// user obtained via the OAuth 1.0a 3-legged flow. AccessToken/AccessSecret are
// the user-context credentials used to sign POST /2/tweets calls.
type XAccount struct {
	UserPubkey    string     `json:"user_pubkey"`
	TwitterUserID string     `json:"twitter_user_id"`
	ScreenName    string     `json:"screen_name"`
	AccessToken   string     `json:"access_token"`
	AccessSecret  string     `json:"access_secret"`
	Enabled       bool       `json:"enabled"`
	LinkedAt      time.Time  `json:"linked_at"`
	LastPostedAt  *time.Time `json:"last_posted_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	// IDs of nostr events already crossposted, capped for dedupe.
	PostedIDs []string `json:"posted_ids,omitempty"`
}

// copy returns a deep copy so callers can't mutate store state.
func (a *XAccount) copy() *XAccount {
	c := *a
	if a.LastPostedAt != nil {
		t := *a.LastPostedAt
		c.LastPostedAt = &t
	}
	if a.PostedIDs != nil {
		c.PostedIDs = make([]string, len(a.PostedIDs))
		copy(c.PostedIDs, a.PostedIDs)
	}
	return &c
}

const maxPostedIDs = 500

func (a *XAccount) markPosted(eventID string, at time.Time) {
	if a.hasPosted(eventID) {
		return
	}
	a.PostedIDs = append(a.PostedIDs, eventID)
	if len(a.PostedIDs) > maxPostedIDs {
		a.PostedIDs = a.PostedIDs[len(a.PostedIDs)-maxPostedIDs:]
	}
	a.LastPostedAt = &at
	a.LastError = ""
}

func (a *XAccount) hasPosted(eventID string) bool {
	for _, id := range a.PostedIDs {
		if id == eventID {
			return true
		}
	}
	return false
}

// XAccountStore persists linked X accounts to a JSON file, same pattern as
// SchedulerStore. Secret material stays server-side on disk; it is never
// exposed through the API (status endpoint strips it).
type XAccountStore struct {
	mu       sync.RWMutex
	filePath string
	accounts map[string]*XAccount
}

func NewXAccountStore(dataDir string) (*XAccountStore, error) {
	store := &XAccountStore{
		filePath: filepath.Join(dataDir, "x_accounts.json"),
		accounts: make(map[string]*XAccount),
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &store.accounts); err != nil {
		return nil, fmt.Errorf("parsing x_accounts.json: %w", err)
	}

	return store, nil
}

// saveLocked must be called with s.mu held (read or write).
func (s *XAccountStore) saveLocked() error {
	data, err := json.MarshalIndent(s.accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}

func (s *XAccountStore) Get(pubkey string) *XAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if acct, ok := s.accounts[pubkey]; ok {
		return acct.copy()
	}
	return nil
}

func (s *XAccountStore) Put(acct *XAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[acct.UserPubkey] = acct
	return s.saveLocked()
}

func (s *XAccountStore) Delete(pubkey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, pubkey)
	return s.saveLocked()
}

// Mutate applies fn to the stored account under lock and persists.
func (s *XAccountStore) Mutate(pubkey string, fn func(*XAccount)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acct, ok := s.accounts[pubkey]
	if !ok {
		return fmt.Errorf("account not found")
	}
	fn(acct)
	return s.saveLocked()
}
