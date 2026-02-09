package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	Status       string       `json:"status"` // pending, processing, published, failed
	PublishedAt  *time.Time   `json:"published_at,omitempty"`
	ErrorMessage string       `json:"error_message,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}

// copy returns a deep copy of the ScheduledPost to prevent data races
func (p *ScheduledPost) copy() *ScheduledPost {
	copy := *p
	if copy.SignedEvent != nil {
		eventCopy := *p.SignedEvent
		copy.SignedEvent = &eventCopy
	}
	if copy.PublishedAt != nil {
		t := *p.PublishedAt
		copy.PublishedAt = &t
	}
	if copy.Relays != nil {
		copy.Relays = make([]string, len(p.Relays))
		copyRelays(copy.Relays, p.Relays)
	}
	return &copy
}

func copyRelays(dst, src []string) {
	copy(dst, src)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.posts, "", "  ")
	if err != nil {
		return err
	}

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
		return post.copy(), nil
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
			result = append(result, post.copy())
		}
	}
	return result
}

func (s *SchedulerStore) ListPending() []*ScheduledPost {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*ScheduledPost
	now := time.Now()

	for _, post := range s.posts {
		if post.Status == "pending" &&
			(post.ScheduledFor.Before(now) || post.ScheduledFor.Equal(now)) {
			// Atomically transition to "processing" to prevent duplicate work
			post.Status = "processing"
			result = append(result, post.copy())
		}
	}

	// Persist status changes atomically
	go s.save()

	return result
}

// validateRelayURL performs SSRF protection by validating relay URLs
func validateRelayURL(relayURL string) error {
	// Parse URL to ensure it's well-formed
	u, err := url.Parse(relayURL)
	if err != nil {
		return fmt.Errorf("invalid relay URL: %w", err)
	}

	// Only allow ws://, wss://, http://, https:// schemes
	if u.Scheme != "ws" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported relay scheme: %s", u.Scheme)
	}

	// Block private/local network addresses (basic SSRF protection)
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" ||
		strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "172.16.") ||
		strings.HasPrefix(host, "169.254.") {
		return fmt.Errorf("blocked relay address: %s", host)
	}

	return nil
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

// logWithFields is a simple structured logging helper
func logWithFields(level, message string, fields map[string]interface{}) {
	fieldStrs := make([]string, 0, len(fields))
	for k, v := range fields {
		fieldStrs = append(fieldStrs, fmt.Sprintf("%s=%v", k, v))
	}
	log.Printf("[%s] %s %s", level, message, strings.Join(fieldStrs, " "))
}

func (s *Scheduler) Start() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			s.processPendingPosts()
		}
	}()
	logWithFields("info", "Scheduler started", map[string]interface{}{
		"interval": "1 minute",
	})
}

func (s *Scheduler) processPendingPosts() {
	posts := s.store.ListPending()

	for _, post := range posts {
		go s.publishPost(post)
	}
}

func (s *Scheduler) publishPost(post *ScheduledPost) {
	logWithFields("info", "Publishing scheduled post", map[string]interface{}{
		"post_id":     post.ID,
		"user_pubkey": post.UserPubkey,
	})

	ctx := context.Background()
	successCount := 0
	var lastErr error

	// Publish to local relay first
	if relay != nil {
		added, err := relay.AddEvent(ctx, post.SignedEvent)
		if err != nil {
			logWithFields("error", "Failed to add event to local relay", map[string]interface{}{
				"post_id": post.ID,
				"error":   err.Error(),
			})
			lastErr = err
		} else if added {
			successCount++
			logWithFields("info", "Added event to local relay", map[string]interface{}{
				"post_id": post.ID,
			})
		}
	}

	// Validate and publish to external relays
	for _, relayURL := range post.Relays {
		// SSRF protection: validate relay URL
		if err := validateRelayURL(relayURL); err != nil {
			logWithFields("warn", "Skipping invalid relay URL", map[string]interface{}{
				"post_id":   post.ID,
				"relay_url": relayURL,
				"error":     err.Error(),
			})
			if lastErr == nil {
				lastErr = err
			}
			continue
		}

		r, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			logWithFields("error", "Failed to connect to relay", map[string]interface{}{
				"post_id":   post.ID,
				"relay_url": relayURL,
				"error":     err.Error(),
			})
			lastErr = err
			continue
		}

		err = r.Publish(ctx, *post.SignedEvent)
		r.Close()

		if err != nil {
			logWithFields("error", "Failed to publish to relay", map[string]interface{}{
				"post_id":   post.ID,
				"relay_url": relayURL,
				"error":     err.Error(),
			})
			lastErr = err
			continue
		}

		successCount++
		logWithFields("info", "Published to relay", map[string]interface{}{
			"post_id":   post.ID,
			"relay_url": relayURL,
		})
	}

	// Update status based on actual publish results
	post.Status = "published"
	if successCount == 0 {
		post.Status = "failed"
		if lastErr != nil {
			post.ErrorMessage = "Publish failed"
		} else {
			post.ErrorMessage = "No valid relays specified"
		}
	}

	now := time.Now()
	post.PublishedAt = &now

	if err := s.store.Update(post); err != nil {
		logWithFields("error", "Failed to update post status", map[string]interface{}{
			"post_id": post.ID,
			"status":  post.Status,
			"error":   err.Error(),
		})
	}

	logWithFields("info", "Finished publishing scheduled post", map[string]interface{}{
		"post_id":       post.ID,
		"final_status":  post.Status,
		"success_count": successCount,
	})
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
		logWithFields("warn", "Unauthorized schedule attempt", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse Body
	var req struct {
		SignedEvent  nostr.Event `json:"signed_event"`
		Relays       []string    `json:"relays"`
		ScheduledFor time.Time   `json:"scheduled_for"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logWithFields("warn", "Invalid JSON in schedule request", map[string]interface{}{
			"user_pubkey": userPubkey,
			"error":       err.Error(),
		})
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate User (must match signed event pubkey)
	if req.SignedEvent.PubKey != userPubkey {
		logWithFields("warn", "Event pubkey mismatch", map[string]interface{}{
			"user_pubkey":     userPubkey,
			"event_pubkey":    req.SignedEvent.PubKey,
		})
		http.Error(w, "Event pubkey mismatch", http.StatusBadRequest)
		return
	}

	// Validate signature
	ok, err := req.SignedEvent.CheckSignature()
	if !ok || err != nil {
		logWithFields("warn", "Invalid event signature", map[string]interface{}{
			"user_pubkey": userPubkey,
		})
		http.Error(w, "Invalid event signature", http.StatusBadRequest)
		return
	}

	// Validate relay URLs (SSRF protection)
	for _, relayURL := range req.Relays {
		if err := validateRelayURL(relayURL); err != nil {
			logWithFields("warn", "Invalid relay URL in request", map[string]interface{}{
				"user_pubkey": userPubkey,
				"relay_url":   relayURL,
				"error":       err.Error(),
			})
			http.Error(w, "Invalid relay URL", http.StatusBadRequest)
			return
		}
	}

	// Create ScheduledPost
	post := &ScheduledPost{
		ID:           nostr.GeneratePrivateKey(),
		UserPubkey:   userPubkey,
		Kind:         req.SignedEvent.Kind,
		SignedEvent:  &req.SignedEvent,
		Relays:       req.Relays,
		ScheduledFor: req.ScheduledFor,
		Status:       "pending",
		CreatedAt:    time.Now(),
	}

	if err := s.store.Add(post); err != nil {
		logWithFields("error", "Failed to save scheduled post", map[string]interface{}{
			"user_pubkey": userPubkey,
			"post_id":     post.ID,
			"error":       err.Error(),
		})
		http.Error(w, "Failed to save schedule", http.StatusInternalServerError)
		return
	}

	logWithFields("info", "Scheduled post created", map[string]interface{}{
		"user_pubkey":    userPubkey,
		"post_id":        post.ID,
		"scheduled_for":  req.ScheduledFor,
		"relay_count":    len(req.Relays),
	})

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
		logWithFields("warn", "Unauthorized list attempt", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	posts := s.store.ListByUser(userPubkey)

	logWithFields("info", "Listed scheduled posts", map[string]interface{}{
		"user_pubkey":  userPubkey,
		"post_count":   len(posts),
	})

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
		logWithFields("warn", "Unauthorized delete attempt", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}

	post, err := s.store.Get(id)
	if err != nil {
		logWithFields("warn", "Post not found for deletion", map[string]interface{}{
			"user_pubkey": userPubkey,
			"post_id":     id,
		})
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	if post.UserPubkey != userPubkey {
		logWithFields("warn", "Forbidden deletion attempt", map[string]interface{}{
			"user_pubkey":  userPubkey,
			"post_owner":   post.UserPubkey,
			"post_id":      id,
		})
		http.Error(w, "Not allowed", http.StatusForbidden)
		return
	}

	if err := s.store.Delete(id); err != nil {
		logWithFields("error", "Failed to delete scheduled post", map[string]interface{}{
			"user_pubkey": userPubkey,
			"post_id":     id,
			"error":       err.Error(),
		})
		http.Error(w, "Failed to delete", http.StatusInternalServerError)
		return
	}

	logWithFields("info", "Deleted scheduled post", map[string]interface{}{
		"user_pubkey": userPubkey,
		"post_id":     id,
	})

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

	// Check u tag matches request URL (fix potential panic)
	uTag := event.Tags.GetFirst([]string{"u", ""})
	if uTag == nil || len(*uTag) < 2 {
		return "", fmt.Errorf("missing or malformed u tag in NIP-98 token")
	}
	if (*uTag)[1] != fullURL {
		return "", fmt.Errorf("URL mismatch in NIP-98 token")
	}

	// Check method tag
	methodTag := event.Tags.GetFirst([]string{"method", ""})
	if methodTag == nil || len(*methodTag) < 2 {
		return "", fmt.Errorf("missing or malformed method tag in NIP-98 token")
	}
	if !strings.EqualFold((*methodTag)[1], r.Method) {
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
