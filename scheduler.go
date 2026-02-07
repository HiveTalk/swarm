package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// ScheduledPost represents a post scheduled for future publication
type ScheduledPost struct {
	ID           string       `json:"id"`
	UserPubkey   string       `json:"user_pubkey"`
	Kind         int          `json:"kind"`
	SignedEvent  *nostr.Event `json:"signed_event"`
	Relays       []string     `json:"relays"`
	ScheduledFor time.Time    `json:"scheduled_for"`
	Status       string       `json:"status"` // pending, published, failed
	PublishedAt  *time.Time   `json:"published_at,omitempty"`
	ErrorMessage string       `json:"error_message,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}

// SchedulerStore handles persistence of scheduled posts
type SchedulerStore struct {
	mu       sync.RWMutex
	filePath string
	posts    map[string]*ScheduledPost
}

func NewSchedulerStore(dataDir string) (*SchedulerStore, error) {
	filePath := filepath.Join(dataDir, "scheduled_posts.json")
	store := &SchedulerStore{
		filePath: filePath,
		posts:    make(map[string]*ScheduledPost),
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	// Load existing data
	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *SchedulerStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // New file
		}
		return err
	}

	return json.Unmarshal(data, &s.posts)
}

func (s *SchedulerStore) save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.posts, "", "  ")
	s.mu.RUnlock()

	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *SchedulerStore) Add(post *ScheduledPost) error {
	s.mu.Lock()
	s.posts[post.ID] = post
	s.mu.Unlock()
	return s.save()
}

func (s *SchedulerStore) Get(id string) (*ScheduledPost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if post, ok := s.posts[id]; ok {
		return post, nil
	}
	return nil, fmt.Errorf("post not found")
}

func (s *SchedulerStore) Update(post *ScheduledPost) error {
	s.mu.Lock()
	s.posts[post.ID] = post
	s.mu.Unlock()
	return s.save()
}

func (s *SchedulerStore) Delete(id string) error {
	s.mu.Lock()
	delete(s.posts, id)
	s.mu.Unlock()
	return s.save()
}

func (s *SchedulerStore) ListByUser(pubkey string) []*ScheduledPost {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ScheduledPost
	for _, post := range s.posts {
		if post.UserPubkey == pubkey {
			result = append(result, post)
		}
	}
	return result
}

func (s *SchedulerStore) ListPending() []*ScheduledPost {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ScheduledPost
	for _, post := range s.posts {
		if post.Status == "pending" {
			result = append(result, post)
		}
	}
	return result
}

// Scheduler handles the background processing of scheduled posts
type Scheduler struct {
	store *SchedulerStore
}

func NewScheduler(dataDir string) (*Scheduler, error) {
	store, err := NewSchedulerStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Scheduler{store: store}, nil
}

func (s *Scheduler) Start() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			s.processPendingPosts()
		}
	}()
	log.Println("Scheduler started, checking every minute")
}

func (s *Scheduler) processPendingPosts() {
	posts := s.store.ListPending()
	now := time.Now()

	for _, post := range posts {
		if post.ScheduledFor.Before(now) || post.ScheduledFor.Equal(now) {
			go s.publishPost(post)
		}
	}
}

func (s *Scheduler) publishPost(post *ScheduledPost) {
	log.Printf("Publishing scheduled post %s...", post.ID)

	// Publish to local relay
	ctx := context.Background()

	// We need to add the event to the relay
	// using the existing global 'relay' variable from main.go
	if relay != nil {
		// Use StoreEvent which goes through the full pipeline
		// But wait, StoreEvent might reject if it thinks it's spam or unauthorized?
		// Since it's a signed event by a valid user, it should be fine.
		// However, typical AddEvent flow is better.
		// relay.AddEvent(ctx, post.SignedEvent)
		// But let's look at how main.go uses db.SaveEvent directly or relay.ReceiveEvent

		// Actually, we can just treat it as an incoming event
		// But since we are internal, we can skip some checks if we want,
		// but safer to go through normal channels.
		// Let's try to add it to the database directly if 'relay' isn't easily accessible for injection
		// But 'relay' IS global in main.go (which this file is part of).

		// Wait, relay.AddEvent is for internal add.
		added, err := relay.AddEvent(ctx, post.SignedEvent)
		if err != nil {
			log.Printf("Failed to add event to local relay: %v", err)
		} else {
			log.Printf("Added event to local relay, status: %v", added)
		}
	}

	// Publish to other relays
	successCount := 0
	var lastErr error

	for _, relayURL := range post.Relays {
		// Skip if it's the local relay (we already added it)
		// TODO: simple check?

		r, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			lastErr = err
			continue
		}

		err = r.Publish(ctx, *post.SignedEvent)
		r.Close()

		if err != nil {
			log.Printf("Failed to publish to relay %s: %v", relayURL, err)
			lastErr = err
			continue
		}

		successCount++
	}

	// Update status
	post.Status = "published"
	if successCount == 0 && len(post.Relays) > 0 {
		post.Status = "failed"
		if lastErr != nil {
			post.ErrorMessage = lastErr.Error()
		} else {
			post.ErrorMessage = "Unknown error"
		}
	}

	now := time.Now()
	post.PublishedAt = &now

	s.store.Update(post)
}

// HTTP Handlers

func (s *Scheduler) enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func (s *Scheduler) HandleSchedule(w http.ResponseWriter, r *http.Request) {
	s.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Auth Check (NIP-98)
	userPubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Parse Body
	var req struct {
		SignedEvent  nostr.Event `json:"signed_event"`
		Relays       []string    `json:"relays"`
		ScheduledFor time.Time   `json:"scheduled_for"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate User (must match signed event pubkey)
	if req.SignedEvent.PubKey != userPubkey {
		http.Error(w, "Event pubkey mismatch", http.StatusBadRequest)
		return
	}

	// Validate signature
	ok, err := req.SignedEvent.CheckSignature()
	if !ok || err != nil {
		http.Error(w, "Invalid event signature", http.StatusBadRequest)
		return
	}

	// Create ScheduledPost
	post := &ScheduledPost{
		ID:           nostr.GeneratePrivateKey(), // Use a random ID or unique enough string
		UserPubkey:   userPubkey,
		Kind:         req.SignedEvent.Kind,
		SignedEvent:  &req.SignedEvent,
		Relays:       req.Relays,
		ScheduledFor: req.ScheduledFor,
		Status:       "pending",
		CreatedAt:    time.Now(),
	}

	if err := s.store.Add(post); err != nil {
		http.Error(w, "Failed to save schedule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(post)
}

func (s *Scheduler) HandleList(w http.ResponseWriter, r *http.Request) {
	s.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userPubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	posts := s.store.ListByUser(userPubkey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(posts)
}

func (s *Scheduler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	s.enableCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userPubkey, err := checkAuth(r)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}

	post, err := s.store.Get(id)
	if err != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	if post.UserPubkey != userPubkey {
		http.Error(w, "Not allowed", http.StatusForbidden)
		return
	}

	if err := s.store.Delete(id); err != nil {
		http.Error(w, "Failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// CheckAuth verifies NIP-98 header and checks if user is allowed in nostr.json
func checkAuth(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing Authorization header")
	}

	// Parse NIP-98 token inline (no nip98 subpackage available)
	prefix := "Nostr "
	if !strings.HasPrefix(authHeader, prefix) || len(authHeader) <= len(prefix) {
		return "", fmt.Errorf("invalid header format")
	}
	token := authHeader[len(prefix):]

	// Decode base64 event
	eventJSON, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("invalid base64 token: %w", err)
	}

	var event nostr.Event
	if err := json.Unmarshal(eventJSON, &event); err != nil {
		return "", fmt.Errorf("invalid event JSON: %w", err)
	}

	// Validate NIP-98 requirements
	if event.Kind != 27235 {
		return "", fmt.Errorf("invalid event kind for NIP-98: %d", event.Kind)
	}

	ok, err := event.CheckSignature()
	if !ok || err != nil {
		return "", fmt.Errorf("invalid event signature")
	}

	// Reconstruct full request URL for comparison
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwdProto := r.Header.Get("X-Forwarded-Proto"); fwdProto != "" {
		scheme = fwdProto
	}
	fullURL := scheme + "://" + r.Host + r.URL.RequestURI()

	// Check u tag matches request URL
	uTag := event.Tags.GetFirst([]string{"u", ""})
	if uTag == nil || (*uTag)[1] != fullURL {
		return "", fmt.Errorf("URL mismatch in NIP-98 token: got %s, expected %s", (*uTag)[1], fullURL)
	}

	// Check method tag
	methodTag := event.Tags.GetFirst([]string{"method", ""})
	if methodTag == nil || !strings.EqualFold((*methodTag)[1], r.Method) {
		return "", fmt.Errorf("method mismatch in NIP-98 token")
	}

	pubkey := event.PubKey

	// Check against allowed list (data.Names)
	// Access the global 'data' variable
	allowed := false

	// Check if user is in data.Names
	for _, pk := range data.Names {
		if pk == pubkey {
			allowed = true
			break
		}
	}

	if !allowed {
		return "", fmt.Errorf("pubkey not allowed in nostr.json")
	}

	return pubkey, nil
}
