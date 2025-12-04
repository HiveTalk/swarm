package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiatjaf/eventstore/badger"
	"github.com/fiatjaf/eventstore/lmdb"
	"github.com/fiatjaf/eventstore/postgresql"
	"github.com/fiatjaf/khatru"
	"github.com/fiatjaf/khatru/blossom"
	"github.com/joho/godotenv"
	"github.com/nbd-wtf/go-nostr"
	"github.com/spf13/afero"
)

type Config struct {
	RelayName          string
	RelayPubkey        string
	RelayDescription   string
	DBEngine           *string
	DBPath             *string
	PostgresUser       *string
	PostgresPassword   *string
	PostgresDB         *string
	PostgresHost       *string
	PostgresPort       *string
	TeamDomain         string
	NPUBDomain         string
	BlossomEnabled     bool
	BlossomPath        *string
	BlossomURL         *string
	WebSocketURL       *string
	AllowedKinds       []int
	PublicAllowedKinds []int
	TrustedClientName  string
	TrustedClientKinds []int
	MaxUploadSizeMB    int
	RelayPort          string
	AllowedMirrorHosts []string
}

type NostrData struct {
	Names  map[string]string   `json:"names"`
	Relays map[string][]string `json:"relays"`
}

var data NostrData
var relay *khatru.Relay
var db DBBackend
var fs afero.Fs
var config Config

func main() {
	relay = khatru.NewRelay()
	config := LoadConfig()

	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)
	relay.DeleteEvent = append(relay.DeleteEvent, db.DeleteEvent)

	fetchNostrData(config.NPUBDomain)

	// Apply spam protection policies
	applySpamProtection(relay, config)

	go func() {
		for {
			time.Sleep(1 * time.Hour)
			fetchNostrData(config.NPUBDomain)
		}
	}()

	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (reject bool, msg string) {
		// Check for trusted client exception: allow specific kinds from a specific client
		trustedClientException := false
		if config.TrustedClientName != "" && len(config.TrustedClientKinds) > 0 {
			for _, tag := range event.Tags {
				if len(tag) >= 2 && tag[0] == "client" && tag[1] == config.TrustedClientName {
					for _, kc := range config.TrustedClientKinds {
						if event.Kind == kc {
							trustedClientException = true
							break
						}
					}
					if trustedClientException {
						break
					}
				}
			}
		}
		if trustedClientException {
			return false, "" // allow event from trusted client for configured kinds
		}

		// Check if this is a delete event (kind 5)
		if event.Kind == 5 {
			// Team members can delete any events
			for _, pubkey := range data.Names {
				if event.PubKey == pubkey {
					return false, "" // allow team members to delete any events
				}
			}

			// Public users can delete their own posts if they have "e" tags referencing events
			// and the original event was posted via PUBLIC_ALLOWED_KINDS
			if len(config.PublicAllowedKinds) > 0 {
				// Check if the delete event has "e" tags (references to events being deleted)
				hasEventRefs := false
				for _, tag := range event.Tags {
					if len(tag) >= 2 && tag[0] == "e" {
						hasEventRefs = true
						break
					}
				}

				if hasEventRefs {
					// Allow public users to delete (they can only delete their own events
					// as the relay will verify ownership when processing the delete)
					return false, "" // allow public users to delete their own events
				}
			}

			return true, "only team members can delete events, or users can delete their own posts"
		}

		// Check if this is a public allowed kind (any pubkey can post these)
		if len(config.PublicAllowedKinds) > 0 {
			for _, publicKind := range config.PublicAllowedKinds {
				if event.Kind == publicKind {
					return false, "" // allow public posting for this kind
				}
			}
		}

		// Check if user is part of the team
		isTeamMember := false
		for _, pubkey := range data.Names {
			if event.PubKey == pubkey {
				isTeamMember = true
				break
			}
		}
		if !isTeamMember {
			return true, "you are not part of the team"
		}

		// Check if event kind is allowed for team members
		if len(config.AllowedKinds) > 0 {
			isKindAllowed := false
			for _, allowedKind := range config.AllowedKinds {
				if event.Kind == allowedKind {
					isKindAllowed = true
					break
				}
			}
			if !isKindAllowed {
				return true, fmt.Sprintf("event kind %d is not allowed for team members", event.Kind)
			}
		}

		return false, "" // allow
	})

	// Setup front page handler
	setupFrontPageHandler(relay, config)

	// Add handler for all public assets
	relay.Router().HandleFunc("/public/", func(w http.ResponseWriter, r *http.Request) {
		// Get the requested file path (remove /public/ prefix)
		requestedPath := strings.TrimPrefix(r.URL.Path, "/public/")

		// Prevent directory traversal attacks
		if strings.Contains(requestedPath, "..") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		// Serve the file from public directory
		filePath := "./public/" + requestedPath
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}

		http.ServeFile(w, r, filePath)
	})

	// Serve Bouquet client static files
	setupBouquetHandler(relay)

	if !config.BlossomEnabled {
		// Configure HTTP server with timeouts suitable for large file uploads
		server := &http.Server{
			Addr:              ":" + config.RelayPort,
			Handler:           relay,
			ReadTimeout:       15 * time.Minute, // Increased to 15 minutes for very large files
			WriteTimeout:      15 * time.Minute, // Increased to 15 minutes
			IdleTimeout:       5 * time.Minute,  // Increased idle timeout
			ReadHeaderTimeout: 30 * time.Second, // Prevent slow header attacks
			MaxHeaderBytes:    1 << 20,          // 1MB max header size
		}

		fmt.Println("running on :" + config.RelayPort + " with extended timeouts for large uploads")
		server.ListenAndServe()
		return
	}

	bl := blossom.New(relay, *config.BlossomURL)
	bl.Store = blossom.EventStoreBlobIndexWrapper{Store: db, ServiceURL: bl.ServiceURL}
	bl.StoreBlob = append(bl.StoreBlob, func(ctx context.Context, sha256 string, body []byte) error {
		// Create context with timeout for large file operations
		storeCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		file, err := fs.Create(*config.BlossomPath + sha256)
		if err != nil {
			return err
		}
		defer file.Close()

		// Use streaming copy with context checking for large files
		reader := bytes.NewReader(body)
		buffer := make([]byte, 32*1024) // 32KB buffer for efficient copying

		for {
			select {
			case <-storeCtx.Done():
				return storeCtx.Err()
			default:
			}

			n, err := reader.Read(buffer)
			if n > 0 {
				if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
					return writeErr
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}

		return file.Sync() // Ensure data is written to disk
	})

	bl.LoadBlob = append(bl.LoadBlob, func(ctx context.Context, sha256 string) (io.ReadSeeker, error) {
		filePath := *config.BlossomPath + sha256
		log.Printf("LoadBlob: Attempting to open file at path: %s", filePath)
		file, err := fs.Open(filePath)
		if err != nil {
			log.Printf("LoadBlob: Failed to open file %s: %v", filePath, err)
			return nil, err
		}
		log.Printf("LoadBlob: Successfully opened file %s", filePath)
		return file, nil
	})
	bl.DeleteBlob = append(bl.DeleteBlob, func(ctx context.Context, sha256 string) error {
		return fs.Remove(*config.BlossomPath + sha256)
	})
	bl.RejectUpload = append(bl.RejectUpload, func(ctx context.Context, event *nostr.Event, size int, ext string) (bool, string, int) {
		// Check for configurable size limit
		maxSize := config.MaxUploadSizeMB * 1024 * 1024
		if size > maxSize {
			return true, fmt.Sprintf("file size exceeds %dMB limit", config.MaxUploadSizeMB), 413
		}

		for _, pubkey := range data.Names {
			if pubkey == event.PubKey {
				return false, ext, size
			}
		}

		return true, "you are not part of the team", 403
	})

	// Add custom list endpoint for Sakura health checks
	relay.Router().HandleFunc("/list/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract pubkey from URL path
		pubkey := strings.TrimPrefix(r.URL.Path, "/list/")
		if pubkey == "" {
			http.Error(w, "Missing pubkey", http.StatusBadRequest)
			return
		}

		log.Printf("List blobs request for pubkey: %s", pubkey)

		// Read all files from the blossom directory
		blobs := []map[string]interface{}{}

		if config.BlossomPath != nil {
			file, err := fs.Open(*config.BlossomPath)
			if err != nil {
				log.Printf("Error opening blossom directory: %v", err)
			} else {
				defer file.Close()
				fileInfos, err := file.Readdir(-1)
				if err != nil {
					log.Printf("Error reading blossom directory: %v", err)
				} else {
					for _, fileInfo := range fileInfos {
						if !fileInfo.IsDir() {
							fileName := fileInfo.Name()
							// Validate that it looks like a SHA256 hash (64 hex characters)
							if len(fileName) == 64 {
								isValidHash := true
								for _, char := range fileName {
									if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
										isValidHash = false
										break
									}
								}

								if isValidHash {
									// Detect MIME type by reading the first 512 bytes
									contentType := "application/octet-stream" // Default fallback
									filePath := *config.BlossomPath + fileName
									if blobFile, err := fs.Open(filePath); err == nil {
										buffer := make([]byte, 512)
										if n, err := blobFile.Read(buffer); err == nil && n > 0 {
											detectedType := http.DetectContentType(buffer[:n])
											if detectedType != "" {
												contentType = detectedType
											}
										}
										blobFile.Close()
									}

									blob := map[string]interface{}{
										"sha256":   strings.ToLower(fileName),
										"size":     fileInfo.Size(),
										"type":     contentType,
										"url":      *config.BlossomURL + "/" + strings.ToLower(fileName),
										"uploaded": fileInfo.ModTime().Unix(),
									}
									blobs = append(blobs, blob)
									log.Printf("Found blob: %s (size: %d, type: %s)", fileName, fileInfo.Size(), contentType)
								}
							}
						}
					}
				}
			}
		}

		log.Printf("Returning %d blobs for pubkey %s", len(blobs), pubkey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(blobs)
	})

	// Add custom mirror endpoint handler for Sakura compatibility
	relay.Router().HandleFunc("/mirror", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse the request body to get source URL
		var mirrorRequest struct {
			URL string `json:"url"`
		}

		if err := json.NewDecoder(r.Body).Decode(&mirrorRequest); err != nil {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
			return
		}

		if mirrorRequest.URL == "" {
			http.Error(w, "Missing source URL", http.StatusBadRequest)
			return
		}

		// Validate URL against allowlist to prevent SSRF attacks
		if !isAllowedMirrorURL(mirrorRequest.URL, config.AllowedMirrorHosts) {
			http.Error(w, "Source URL host not in allowed list", http.StatusForbidden)
			return
		}

		// Store validated URL to make it clear to static analysis that it's safe
		validatedURL := mirrorRequest.URL

		// Extract blob hash from source URL
		blobHash := extractSha256FromURL(validatedURL)
		if blobHash == "" {
			http.Error(w, "Cannot extract blob hash from source URL", http.StatusBadRequest)
			return
		}

		// Check if blob already exists
		if _, err := fs.Open(*config.BlossomPath + blobHash); err == nil {
			// Blob already exists, return success
			response := map[string]interface{}{
				"sha256": blobHash,
				"url":    *config.BlossomURL + "/" + blobHash,
				"size":   0, // We don't know the size without reading the file
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		// Download blob from validated source URL
		resp, err := http.Get(validatedURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch source blob: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("Source server returned %d", resp.StatusCode), http.StatusBadGateway)
			return
		}

		// Read and verify the blob content
		blobData, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read blob data: %v", err), http.StatusInternalServerError)
			return
		}

		// Verify the hash matches
		hasher := sha256.New()
		hasher.Write(blobData)
		actualHash := hex.EncodeToString(hasher.Sum(nil))

		if actualHash != blobHash {
			http.Error(w, "Blob hash mismatch", http.StatusBadRequest)
			return
		}

		// Store the blob using the existing StoreBlob functionality
		ctx := r.Context()
		for _, storeFunc := range bl.StoreBlob {
			if err := storeFunc(ctx, blobHash, blobData); err != nil {
				http.Error(w, fmt.Sprintf("Failed to store blob: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// Return success response
		response := map[string]interface{}{
			"sha256": blobHash,
			"url":    *config.BlossomURL + "/" + blobHash,
			"size":   len(blobData),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

		log.Printf("Successfully mirrored blob %s from %s", blobHash, validatedURL)
	})

	// Configure HTTP server with timeouts suitable for large file uploads
	server := &http.Server{
		Addr:              ":" + config.RelayPort,
		Handler:           relay,
		ReadTimeout:       15 * time.Minute, // Increased to 15 minutes for very large files
		WriteTimeout:      15 * time.Minute, // Increased to 15 minutes
		IdleTimeout:       5 * time.Minute,  // Increased idle timeout
		ReadHeaderTimeout: 30 * time.Second, // Prevent slow header attacks
		MaxHeaderBytes:    1 << 20,          // 1MB max header size
	}

	fmt.Println("running on :" + config.RelayPort + " with extended timeouts for large uploads")
	server.ListenAndServe()
}

func fetchNostrData(teamDomain string) {
	response, err := http.Get("https://" + teamDomain + "/.well-known/nostr.json")
	if err != nil {
		log.Printf("Error getting well known file: %v", err)
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Printf("Error reading response body: %v", err)
		return
	}

	var newData NostrData
	err = json.Unmarshal(body, &newData)
	if err != nil {
		log.Printf("Error unmarshalling JSON: %v", err)
		return
	}

	data = newData
	for pubkey, names := range data.Names {
		fmt.Println(pubkey, names)
	}

	log.Println("Updated NostrData from .well-known file")
}

func LoadConfig() Config {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading .env file")
	}

	config = Config{
		RelayName:          getEnv("RELAY_NAME"),
		RelayPubkey:        getEnv("RELAY_PUBKEY"),
		RelayDescription:   getEnv("RELAY_DESCRIPTION"),
		DBEngine:           getEnvNullable("DB_ENGINE"),
		DBPath:             getEnvNullable("DB_PATH"),
		PostgresUser:       getEnvNullable("POSTGRES_USER"),
		PostgresPassword:   getEnvNullable("POSTGRES_PASSWORD"),
		PostgresDB:         getEnvNullable("POSTGRES_DB"),
		PostgresHost:       getEnvNullable("POSTGRES_HOST"),
		PostgresPort:       getEnvNullable("POSTGRES_PORT"),
		TeamDomain:         getEnv("TEAM_DOMAIN"),
		NPUBDomain:         getEnv("NPUB_DOMAIN"),
		BlossomEnabled:     getEnvBool("BLOSSOM_ENABLED"),
		BlossomPath:        getEnvNullable("BLOSSOM_PATH"),
		BlossomURL:         getEnvNullable("BLOSSOM_URL"),
		WebSocketURL:       getEnvNullable("WEBSOCKET_URL"),
		AllowedKinds:       parseAllowedKinds(getEnvNullable("ALLOWED_KINDS")),
		PublicAllowedKinds: parseAllowedKinds(getEnvNullable("PUBLIC_ALLOWED_KINDS")),
		TrustedClientName:  getEnvWithDefault("TRUSTED_CLIENT_NAME", ""),
		TrustedClientKinds: parseAllowedKinds(getEnvNullable("TRUSTED_CLIENT_KINDS")),
		MaxUploadSizeMB:    getEnvIntWithDefault("MAX_UPLOAD_SIZE_MB", 200),
		RelayPort:          getEnvWithDefault("RELAY_PORT", "3334"),
		AllowedMirrorHosts: parseAllowedMirrorHosts(getEnvNullable("ALLOWED_MIRROR_HOSTS")),
	}

	relay.Info.Name = config.RelayName
	relay.Info.PubKey = config.RelayPubkey
	relay.Info.Description = config.RelayDescription
	if config.DBPath == nil {
		defaultPath := "db/"
		config.DBPath = &defaultPath
	}

	db = newDBBackend(*config.DBPath)

	if err := db.Init(); err != nil {
		panic(err)
	}

	fs = afero.NewOsFs()
	if config.BlossomEnabled {
		if config.BlossomPath == nil {
			log.Fatalf("Blossom enabled but no path set")
		}
		fs.MkdirAll(*config.BlossomPath, 0755)
	}

	return config
}

// Rate limiting data structures
type rateLimiter struct {
	mu       sync.RWMutex
	counters map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		counters: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *rateLimiter) isAllowed(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Clean old entries
	times := rl.counters[key]
	var validTimes []time.Time
	for _, t := range times {
		if t.After(cutoff) {
			validTimes = append(validTimes, t)
		}
	}

	// Check if under limit
	if len(validTimes) >= rl.limit {
		return false
	}

	// Add current request
	validTimes = append(validTimes, now)
	rl.counters[key] = validTimes

	return true
}

// Global rate limiters
var (
	pubkeyRateLimit = newRateLimiter(50, time.Minute)   // 50 events per minute per pubkey
	ipRateLimit     = newRateLimiter(100, time.Minute)  // 100 events per minute per IP
	connRateLimit   = newRateLimiter(20, time.Minute*2) // 20 connections per 2 minutes per IP
	queryRateLimit  = newRateLimiter(300, time.Minute)  // 300 queries per minute per IP
)

// applySpamProtection applies rate limiting and spam protection policies
func applySpamProtection(relay *khatru.Relay, config Config) {
	// Rate limit events by pubkey (applies to all users)
	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (reject bool, msg string) {
		// Check if user is team member (more lenient limits)
		isTeamMember := false
		for _, pubkey := range data.Names {
			if event.PubKey == pubkey {
				isTeamMember = true
				break
			}
		}

		// Apply stricter rate limits to non-team members
		if !isTeamMember {
			if !pubkeyRateLimit.isAllowed(event.PubKey) {
				return true, "rate-limited: too many events from this pubkey, slow down please"
			}
		}

		return false, ""
	})

	// Rate limit events by IP
	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (reject bool, msg string) {
		ip := khatru.GetIP(ctx)
		if ip != "" && !ipRateLimit.isAllowed(ip) {
			return true, "rate-limited: too many events from this IP, slow down please"
		}
		return false, ""
	})

	// Rate limit connections
	relay.RejectConnection = append(relay.RejectConnection, func(r *http.Request) bool {
		ip := khatru.GetIPFromRequest(r)
		return !connRateLimit.isAllowed(ip)
	})

	// Rate limit queries/filters
	relay.RejectFilter = append(relay.RejectFilter, func(ctx context.Context, filter nostr.Filter) (reject bool, msg string) {
		ip := khatru.GetIP(ctx)
		if ip != "" && !queryRateLimit.isAllowed(ip) {
			return true, "rate-limited: too many queries from this IP"
		}
		return false, ""
	})

	// Reject events with base64 media (common spam vector)
	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (reject bool, msg string) {
		if strings.Contains(event.Content, "data:image/") || strings.Contains(event.Content, "data:video/") {
			return true, "rejected: base64 media not allowed"
		}
		return false, ""
	})

	log.Println("Applied spam protection policies with rate limiting")
	log.Printf("Rate limits: %d events/min per pubkey, %d events/min per IP", pubkeyRateLimit.limit, ipRateLimit.limit)
}

func getEnv(key string) string {
	value, exists := os.LookupEnv(key)
	if !exists {
		log.Fatalf("Environment variable %s not set", key)
	}
	return value
}

func getEnvBool(key string) bool {
	value, exists := os.LookupEnv(key)
	if !exists {
		return false
	}
	return value == "true"
}

func getEnvNullable(key string) *string {
	value, exists := os.LookupEnv(key)
	if !exists {
		return nil
	}
	return &value
}

func getEnvIntWithDefault(key string, defaultValue int) int {
	value, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("Warning: Invalid integer value '%s' for %s, using default %d", value, key, defaultValue)
		return defaultValue
	}
	return intValue
}

func getEnvWithDefault(key string, defaultValue string) string {
	value, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func parseAllowedKinds(allowedKindsStr *string) []int {
	if allowedKindsStr == nil || strings.TrimSpace(*allowedKindsStr) == "" {
		return []int{} // Empty slice means allow all kinds
	}

	kindsStr := strings.TrimSpace(*allowedKindsStr)
	kindStrings := strings.Split(kindsStr, ",")
	var kinds []int

	for _, kindStr := range kindStrings {
		kindStr = strings.TrimSpace(kindStr)
		if kindStr == "" {
			continue
		}

		kind, err := strconv.Atoi(kindStr)
		if err != nil {
			log.Printf("Warning: Invalid kind '%s' in ALLOWED_KINDS, skipping", kindStr)
			continue
		}
		kinds = append(kinds, kind)
	}

	if len(kinds) > 0 {
		log.Printf("Relay configured to only allow kinds: %v", kinds)
	} else {
		log.Printf("Relay configured to allow all kinds")
	}

	return kinds
}

func parseAllowedMirrorHosts(hostsStr *string) []string {
	if hostsStr == nil || strings.TrimSpace(*hostsStr) == "" {
		return []string{} // Empty slice means mirror endpoint is disabled
	}

	hostsStrVal := strings.TrimSpace(*hostsStr)
	hostStrings := strings.Split(hostsStrVal, ",")
	var hosts []string

	for _, hostStr := range hostStrings {
		hostStr = strings.TrimSpace(hostStr)
		if hostStr == "" {
			continue
		}
		// Normalize: remove trailing slashes and convert to lowercase
		hostStr = strings.ToLower(strings.TrimRight(hostStr, "/"))
		hosts = append(hosts, hostStr)
	}

	if len(hosts) > 0 {
		log.Printf("Mirror endpoint enabled for hosts: %v", hosts)
	} else {
		log.Printf("Mirror endpoint disabled (no allowed hosts configured)")
	}

	return hosts
}

// isAllowedMirrorURL validates that the URL is from an allowed host to prevent SSRF attacks
func isAllowedMirrorURL(rawURL string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 {
		return false // No hosts configured means mirror is disabled
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Only allow http and https schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return false
	}

	// Check if the host matches any allowed host
	host := strings.ToLower(parsedURL.Host)
	for _, allowedHost := range allowedHosts {
		if host == allowedHost {
			return true
		}
	}

	return false
}

type DBBackend interface {
	Init() error
	Close()
	CountEvents(ctx context.Context, filter nostr.Filter) (int64, error)
	DeleteEvent(ctx context.Context, evt *nostr.Event) error
	QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)
	SaveEvent(ctx context.Context, evt *nostr.Event) error
	ReplaceEvent(ctx context.Context, evt *nostr.Event) error
}

func newDBBackend(path string) DBBackend {
	if config.DBEngine == nil {
		defaultEngine := "postgres"
		config.DBEngine = &defaultEngine
	}

	switch *config.DBEngine {
	case "lmdb":
		return newLMDBBackend(path)
	case "badger":
		return &badger.BadgerBackend{
			Path: path,
		}
	default:
		return newPostgresBackend()
	}
}

func newLMDBBackend(path string) *lmdb.LMDBBackend {
	return &lmdb.LMDBBackend{
		Path: path,
	}
}

func newPostgresBackend() DBBackend {
	return &postgresql.PostgresBackend{
		DatabaseURL: fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			*config.PostgresUser, *config.PostgresPassword, *config.PostgresHost, *config.PostgresPort, *config.PostgresDB),
	}
}

// extractSha256FromURL extracts the SHA256 hash from a blossom URL
// Expected format: https://server.com/sha256hash or https://server.com/sha256hash.ext
func extractSha256FromURL(url string) string {
	// Remove the protocol and domain
	parts := strings.Split(url, "/")
	if len(parts) < 4 {
		return ""
	}

	// Get the last part which should be the hash (possibly with extension)
	hashPart := parts[len(parts)-1]

	// Remove file extension if present
	if dotIndex := strings.LastIndex(hashPart, "."); dotIndex != -1 {
		hashPart = hashPart[:dotIndex]
	}

	// Validate that it looks like a SHA256 hash (64 hex characters)
	if len(hashPart) == 64 {
		// Check if all characters are valid hex
		for _, char := range hashPart {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return ""
			}
		}
		return strings.ToLower(hashPart)
	}

	return ""
}

// setupBouquetHandler serves the Bouquet client static files with SPA support
func setupBouquetHandler(relay *khatru.Relay) {
	// Handle /bouquet/ routes with custom SPA handler
	relay.Router().HandleFunc("/bouquet/", func(w http.ResponseWriter, r *http.Request) {
		// Get the requested file path (remove /bouquet/ prefix)
		requestedPath := strings.TrimPrefix(r.URL.Path, "/bouquet/")

		// If no path or it's a directory, serve index.html
		if requestedPath == "" {
			requestedPath = "index.html"
		}

		// Check if the requested file exists
		filePath := "./bouquet-dist/" + requestedPath
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			// File doesn't exist, this is likely a client-side route
			// Serve index.html to let React Router handle it
			http.ServeFile(w, r, "./bouquet-dist/index.html")
			return
		}

		// File exists, serve it directly
		http.ServeFile(w, r, filePath)
	})

	// Handle /bouquet (without trailing slash) - redirect to /bouquet/
	relay.Router().HandleFunc("/bouquet", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/bouquet/", http.StatusMovedPermanently)
	})
}
