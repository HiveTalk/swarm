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
	"path/filepath"
	"regexp"
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
	RelayName             string
	RelayPubkey           string
	RelayDescription      string
	DBEngine              *string
	DBPath                *string
	PostgresUser          *string
	PostgresPassword      *string
	PostgresDB            *string
	PostgresHost          *string
	PostgresPort          *string
	DatabaseURL           *string
	TeamDomain            string
	NPUBDomain            string
	BlossomEnabled        bool
	BlossomPath           *string
	BlossomURL            *string
	WebSocketURL          *string
	AllowedKinds          []int
	PublicAllowedKinds    []int
	TrustedClientName     string
	TrustedClientKinds    []int
	TrustedClientAllKinds bool
	MaxUploadSizeMB       int
	RelayPort             string
	AllowedMirrorHosts    []string
	// S3 Storage Configuration
	StorageBackend string
	S3Endpoint     string
	S3Bucket       string
	S3Region       string
	S3PublicURL    string
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
var s3Storage *S3Storage

func main() {
	relay = khatru.NewRelay()
	config := LoadConfig()

	// Initialize nostr.json with relay pubkey as root if needed
	if err := initializeNostrJson(config); err != nil {
		log.Printf("Warning: Failed to initialize nostr.json: %s", err)
	}

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
		// Check for trusted client exception: allow specific kinds (or all kinds) from a specific client
		trustedClientException := false
		if config.TrustedClientName != "" {
			for _, tag := range event.Tags {
				if len(tag) >= 2 && tag[0] == "client" && tag[1] == config.TrustedClientName {
					// If all kinds allowed for trusted client, allow immediately
					if config.TrustedClientAllKinds {
						trustedClientException = true
						break
					}
					// Otherwise check specific kinds
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

	// Setup dashboard handlers
	setupDashboardHandlers(relay, config)

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

	setupConvertHandlers(relay, config)

	// Add NIP-05 service handlers
	//	setupNIP05Handlers(relay, config)

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

	if config.StorageBackend == "s3" && s3Storage != nil {
		// S3 Storage Backend
		bl.StoreBlob = append(bl.StoreBlob, func(ctx context.Context, sha256 string, body []byte) error {
			return s3Storage.StoreBlob(ctx, sha256, body)
		})

		bl.LoadBlob = append(bl.LoadBlob, func(ctx context.Context, sha256 string) (io.ReadSeeker, error) {
			reader, redirectURL, err := s3Storage.LoadBlob(ctx, sha256)
			if err != nil {
				return nil, err
			}
			// If we have a redirect URL, we need to handle it differently
			// The khatru blossom library expects just ReadSeeker, so we return the reader
			// For S3 with public URL, the redirect is handled via the public URL config
			if redirectURL != nil {
				log.Printf("LoadBlob: S3 redirect URL available: %s", redirectURL.String())
			}
			return reader, nil
		})

		bl.DeleteBlob = append(bl.DeleteBlob, func(ctx context.Context, sha256 string) error {
			return s3Storage.DeleteBlob(ctx, sha256)
		})
	} else {
		// Filesystem Storage Backend
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
	}
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

		// Read all files from storage backend
		blobs := []map[string]interface{}{}

		if config.StorageBackend == "s3" && s3Storage != nil {
			// S3 Storage Backend
			s3Blobs, err := s3Storage.ListBlobs(r.Context())
			if err != nil {
				log.Printf("Error listing S3 blobs: %v", err)
			} else {
				for _, blob := range s3Blobs {
					blobs = append(blobs, map[string]interface{}{
						"sha256":   blob.SHA256,
						"size":     blob.Size,
						"type":     blob.Type,
						"url":      blob.URL,
						"uploaded": blob.Uploaded,
					})
				}
			}
		} else if config.BlossomPath != nil {
			// Filesystem Storage Backend
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

func fetchNostrData(npubDomain string) {
	var body []byte
	var err error

	if npubDomain == "" {
		// Fall back to local file
		body, err = os.ReadFile("./public/.well-known/nostr.json")
		if err != nil {
			log.Printf("Error reading local nostr.json: %v", err)
			return
		}
		log.Println("Using local public/.well-known/nostr.json")
	} else {
		// Fetch from remote domain
		// First try /public/.well-known/nostr.json
		urls := []string{
			"https://" + npubDomain + "/public/.well-known/nostr.json",
			"https://" + npubDomain + "/.well-known/nostr.json",
		}

		var lastErr error
		for _, url := range urls {
			response, err := http.Get(url)
			if err != nil {
				lastErr = err
				continue
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
				continue
			}

			body, err = io.ReadAll(response.Body)
			if err != nil {
				lastErr = err
				continue
			}

			// Basic JSON validation
			if len(body) > 0 && body[0] == '{' {
				log.Printf("Successfully fetched nostr.json from %s", url)
				lastErr = nil
				break
			}
			lastErr = fmt.Errorf("invalid JSON response from %s", url)
		}

		if lastErr != nil {
			log.Printf("Error fetching nostr.json from %s: %v", npubDomain, lastErr)
			return
		}
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

	if npubDomain == "" {
		log.Println("Updated NostrData from local .well-known file")
	} else {
		log.Println("Updated NostrData from remote .well-known file")
	}
}

func LoadConfig() Config {
	// Load .env file if it exists, but don't overwrite existing environment variables
	// This allows docker-compose environment variables to take precedence
	if envMap, err := godotenv.Read(".env"); err == nil {
		for key, value := range envMap {
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}

	config = Config{
		RelayName:             getEnv("RELAY_NAME"),
		RelayPubkey:           getEnv("RELAY_PUBKEY"),
		RelayDescription:      getEnv("RELAY_DESCRIPTION"),
		DBEngine:              getEnvNullable("DB_ENGINE"),
		DBPath:                getEnvNullable("DB_PATH"),
		PostgresUser:          getEnvNullable("POSTGRES_USER"),
		PostgresPassword:      getEnvNullable("POSTGRES_PASSWORD"),
		PostgresDB:            getEnvNullable("POSTGRES_DB"),
		PostgresHost:          getEnvNullable("POSTGRES_HOST"),
		PostgresPort:          getEnvNullable("POSTGRES_PORT"),
		DatabaseURL:           getEnvNullable("DATABASE_URL"),
		TeamDomain:            getEnvWithDefault("TEAM_DOMAIN", ""),
		NPUBDomain:            getEnvWithDefault("NPUB_DOMAIN", ""),
		BlossomEnabled:        getEnvBool("BLOSSOM_ENABLED"),
		BlossomPath:           getEnvWithDefaultPtr("BLOSSOM_PATH", "blossom/"),
		BlossomURL:            getEnvWithDefaultPtr("BLOSSOM_URL", "http://localhost:3334"),
		WebSocketURL:          getEnvWithDefaultPtr("WEBSOCKET_URL", "wss://localhost:3334"),
		AllowedKinds:          parseAllowedKinds(getEnvNullable("ALLOWED_KINDS")),
		PublicAllowedKinds:    parseAllowedKinds(getEnvNullable("PUBLIC_ALLOWED_KINDS")),
		TrustedClientName:     getEnvWithDefault("TRUSTED_CLIENT_NAME", ""),
		TrustedClientKinds:    parseTrustedClientKinds(getEnvNullable("TRUSTED_CLIENT_KINDS")),
		TrustedClientAllKinds: isTrustedClientAllKinds(getEnvNullable("TRUSTED_CLIENT_KINDS")),
		MaxUploadSizeMB:       getEnvIntWithDefault("MAX_UPLOAD_SIZE_MB", 200),
		RelayPort:             getEnvWithDefault("RELAY_PORT", "3334"),
		AllowedMirrorHosts:    parseAllowedMirrorHosts(getEnvNullable("ALLOWED_MIRROR_HOSTS")),
		// S3 Storage Configuration
		StorageBackend: getEnvWithDefault("STORAGE_BACKEND", "filesystem"),
		S3Endpoint:     getEnvWithDefault("S3_ENDPOINT", ""),
		S3Bucket:       getEnvWithDefault("S3_BUCKET", ""),
		S3Region:       getEnvWithDefault("S3_REGION", "auto"),
		S3PublicURL:    getEnvWithDefault("S3_PUBLIC_URL", ""),
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
		if config.StorageBackend == "s3" {
			// Initialize S3 storage
			s3Cfg := getS3ConfigFromEnv()
			if s3Cfg == nil {
				log.Fatalf("S3 storage backend selected but missing required environment variables (S3_ENDPOINT, S3_BUCKET, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)")
			}
			s3Cfg.ServiceURL = *config.BlossomURL
			var err error
			s3Storage, err = NewS3Storage(*s3Cfg)
			if err != nil {
				log.Fatalf("Failed to initialize S3 storage: %v", err)
			}
			log.Printf("Blossom using S3 storage backend: %s/%s", s3Cfg.Endpoint, s3Cfg.Bucket)
		} else {
			// Filesystem storage
			if config.BlossomPath == nil {
				log.Fatalf("Blossom enabled but no path set")
			}
			fs.MkdirAll(*config.BlossomPath, 0755)
			log.Printf("Blossom using filesystem storage backend: %s", *config.BlossomPath)
		}
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

func getEnvWithDefaultPtr(key string, defaultValue string) *string {
	value, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(value) == "" {
		return &defaultValue
	}
	return &value
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

func isTrustedClientAllKinds(kindsStr *string) bool {
	if kindsStr == nil {
		return false
	}
	return strings.TrimSpace(strings.ToLower(*kindsStr)) == "all"
}

func parseTrustedClientKinds(kindsStr *string) []int {
	if kindsStr == nil || strings.TrimSpace(*kindsStr) == "" {
		return []int{}
	}
	// If "all" is specified, return empty slice (TrustedClientAllKinds flag handles this)
	if strings.TrimSpace(strings.ToLower(*kindsStr)) == "all" {
		log.Println("Trusted client configured to allow ALL kinds")
		return []int{}
	}
	return parseAllowedKinds(kindsStr)
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
	var dbURL string
	if config.DatabaseURL != nil && *config.DatabaseURL != "" {
		dbURL = *config.DatabaseURL
		log.Println("Using DATABASE_URL for postgres connection")
	} else {
		dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			*config.PostgresUser, *config.PostgresPassword, *config.PostgresHost, *config.PostgresPort, *config.PostgresDB)
		log.Println("Using individual POSTGRES_* variables for postgres connection")
	}
	return &postgresql.PostgresBackend{
		DatabaseURL: dbURL,
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

func setupConvertHandlers(relay *khatru.Relay, config Config) {
	// Serve the NIP-05 registration page
	relay.Router().HandleFunc("/convert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.ServeFile(w, r, "./public/convert.html")
	})
}

// setupNIP05Handlers adds the NIP-05 service endpoints
// func setupNIP05Handlers(relay *khatru.Relay, config Config) {
// 	// Serve the NIP-05 registration page
// 	relay.Router().HandleFunc("/add-nip05", func(w http.ResponseWriter, r *http.Request) {
// 		if r.Method != "GET" {
// 			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
// 			return
// 		}
// 		http.ServeFile(w, r, "./public/add-nip05.html")
// 	})

// 	// Handle /add-nip05/ (with trailing slash) - redirect to /add-nip05
// 	relay.Router().HandleFunc("/add-nip05/", func(w http.ResponseWriter, r *http.Request) {
// 		http.Redirect(w, r, "/add-nip05", http.StatusMovedPermanently)
// 	})

// 	// API endpoint for NIP-05 submission
// 	relay.Router().HandleFunc("/api/submit-nip05", func(w http.ResponseWriter, r *http.Request) {
// 		// Set CORS headers and content type
// 		w.Header().Set("Access-Control-Allow-Origin", "*")
// 		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
// 		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
// 		w.Header().Set("Content-Type", "application/json")

// 		// Helper function to return JSON errors
// 		returnError := func(message string, statusCode int) {
// 			w.WriteHeader(statusCode)
// 			json.NewEncoder(w).Encode(map[string]interface{}{
// 				"success": false,
// 				"error":   message,
// 			})
// 		}

// 		// Handle CORS preflight
// 		if r.Method == "OPTIONS" {
// 			w.Header().Set("Access-Control-Max-Age", "86400")
// 			w.WriteHeader(http.StatusOK)
// 			return
// 		}

// 		if r.Method != "POST" {
// 			returnError("Method not allowed", http.StatusMethodNotAllowed)
// 			return
// 		}

// 		// Parse request body
// 		var request struct {
// 			Username string `json:"username"`
// 			Pubkey   string `json:"pubkey"`
// 		}

// 		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
// 			returnError("Invalid JSON", http.StatusBadRequest)
// 			return
// 		}

// 		// Validate inputs
// 		username := strings.TrimSpace(strings.ToLower(request.Username))
// 		pubkey := strings.TrimSpace(request.Pubkey)

// 		// Username validation
// 		if !matchUsernamePattern(username) {
// 			returnError("Invalid username format", http.StatusBadRequest)
// 			return
// 		}

// 		// Pubkey validation and conversion
// 		hexPubkey, err := validateAndConvertPubkey(pubkey)
// 		if err != nil {
// 			returnError(err.Error(), http.StatusBadRequest)
// 			return
// 		}

// 		// Update nostr.json file directly in persistent volume
// 		err = updateNostrJson(username, hexPubkey)
// 		if err != nil {
// 			returnError(fmt.Sprintf("Failed to update nostr.json: %s", err.Error()), http.StatusInternalServerError)
// 			return
// 		}

// 		// Return success response
// 		response := map[string]interface{}{
// 			"success":  true,
// 			"message":  fmt.Sprintf("NIP-05 identifier %s@%s has been successfully registered!", username, config.NPUBDomain),
// 			"username": username,
// 			"pubkey":   hexPubkey,
// 			"nip05":    fmt.Sprintf("%s@%s", username, config.NPUBDomain),
// 		}

// 		json.NewEncoder(w).Encode(response)
// 	})
// }

// Helper functions for NIP-05 validation
// func matchUsernamePattern(username string) bool {
// 	if len(username) < 1 || len(username) > 64 {
// 		return false
// 	}
// 	matched, _ := regexp.MatchString(`^[a-z0-9_\.\-]+$`, username)
// 	return matched
// }

func validateAndConvertPubkey(input string) (string, error) {
	input = strings.TrimSpace(input)

	// Check if it's a valid 64-character hex string
	if matched, _ := regexp.MatchString(`^[0-9a-fA-F]{64}$`, input); matched {
		return strings.ToLower(input), nil
	}

	// If it's not hex, try to convert from npub (fallback)
	if strings.HasPrefix(input, "npub1") {
		// Use nostr-tools for npub conversion
		// We'll implement a simple bech32 decoder for npub
		hexKey, err := decodeNpub(input)
		if err != nil {
			return "", fmt.Errorf("invalid npub format: %v", err)
		}
		return hexKey, nil
	}

	return "", fmt.Errorf("invalid public key format - must be npub1... or 64-character hex")
}

// decodeNpub converts npub1... format to hex string
func decodeNpub(npub string) (string, error) {
	if len(npub) != 63 {
		return "", fmt.Errorf("invalid npub length")
	}

	// Remove the "npub1" prefix
	data := npub[5:]

	// Decode from bech32
	converted, err := bech32Decode(data)
	if err != nil {
		return "", err
	}

	// Convert from 5-bit groups to 8-bit groups
	result, err := convertBits(converted, 5, 8, false)
	if err != nil {
		return "", err
	}

	// Verify length (should be 32 bytes for pubkey)
	if len(result) != 32 {
		return "", fmt.Errorf("invalid decoded data length")
	}

	// Convert to hex string
	return fmt.Sprintf("%x", result), nil
}

// Simple bech32 decoder implementation
func bech32Decode(s string) ([]byte, error) {
	// Bech32 character set
	charset := "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

	// Convert string to byte indices
	var converted []byte
	for _, char := range s {
		index := strings.IndexByte(charset, byte(char))
		if index == -1 {
			return nil, fmt.Errorf("invalid character in bech32 string")
		}
		converted = append(converted, byte(index))
	}

	return converted, nil
}

// ConvertBits converts from bit groups of size fromBits to bit groups of size toBits
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint(0)
	bits := uint(0)
	var result []byte
	maxv := uint((1 << toBits) - 1)
	maxAcc := uint((1 << (fromBits + toBits - 1)) - 1)

	for i := 0; i < len(data); i++ {
		value := uint(data[i])
		if (value >> fromBits) != 0 {
			return nil, fmt.Errorf("invalid data range")
		}

		acc = ((acc << fromBits) | value) & maxAcc
		bits += fromBits

		for bits >= toBits {
			bits -= toBits
			result = append(result, byte((acc>>bits)&maxv))
		}
	}

	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("invalid padding")
	}

	return result, nil
}

// updateNostrJson updates the nostr.json file in persistent volume
func updateNostrJson(username, pubkey string) error {
	// Use environment variable or default to local path
	nostrJsonPath := getEnvWithDefault("NIP05_PATH", "public/.well-known/nostr.json")

	// If running in Docker, convert relative path to absolute
	if !filepath.IsAbs(nostrJsonPath) && os.Getenv("DOCKER_ENV") == "true" {
		nostrJsonPath = "/app/" + nostrJsonPath
	}

	// Read existing file
	var nostrData map[string]interface{}

	// Create file if it doesn't exist
	if _, err := os.Stat(nostrJsonPath); os.IsNotExist(err) {
		// Create directory if needed
		if err := os.MkdirAll(filepath.Dir(nostrJsonPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %s", err)
		}

		// Initialize with empty structure
		nostrData = map[string]interface{}{
			"names": map[string]interface{}{},
		}
	} else {
		// Read existing file
		data, err := os.ReadFile(nostrJsonPath)
		if err != nil {
			return fmt.Errorf("failed to read nostr.json: %s", err)
		}

		if err := json.Unmarshal(data, &nostrData); err != nil {
			return fmt.Errorf("failed to parse nostr.json: %s", err)
		}
	}

	// Ensure names object exists
	if nostrData["names"] == nil {
		nostrData["names"] = map[string]interface{}{}
	}

	names, ok := nostrData["names"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid nostr.json structure: names is not an object")
	}

	// Check if username already exists
	if existingPubkey, exists := names[username]; exists {
		if existingPubkey == pubkey {
			return fmt.Errorf("username %s is already registered with this pubkey", username)
		}
		return fmt.Errorf("username %s is already registered with a different pubkey", username)
	}

	// Check if pubkey is already used by another username
	for existingUser, existingPubkey := range names {
		if existingPubkey == pubkey {
			return fmt.Errorf("pubkey is already registered to username %s", existingUser)
		}
	}

	// Add new entry
	names[username] = pubkey
	nostrData["names"] = names

	// Write back to file
	updatedData, err := json.MarshalIndent(nostrData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nostr.json: %s", err)
	}

	if err := os.WriteFile(nostrJsonPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write nostr.json: %s", err)
	}

	log.Printf("Successfully added NIP-05 entry: %s -> %s", username, pubkey)
	return nil
}

// initializeNostrJson creates nostr.json with relay pubkey as root if it doesn't exist
func initializeNostrJson(config Config) error {
	nostrJsonPath := getEnvWithDefault("NIP05_PATH", "public/.well-known/nostr.json")

	// If running in Docker, convert relative path to absolute
	if !filepath.IsAbs(nostrJsonPath) && os.Getenv("DOCKER_ENV") == "true" {
		nostrJsonPath = "/app/" + nostrJsonPath
	}

	// Check if file already exists
	if _, err := os.Stat(nostrJsonPath); err == nil {
		// File exists, check if it has root entry
		data, err := os.ReadFile(nostrJsonPath)
		if err != nil {
			return fmt.Errorf("failed to read existing nostr.json: %s", err)
		}

		var nostrData map[string]interface{}
		if err := json.Unmarshal(data, &nostrData); err != nil {
			return fmt.Errorf("failed to parse existing nostr.json: %s", err)
		}

		// Check if root entry exists
		if names, ok := nostrData["names"].(map[string]interface{}); ok {
			if _, hasRoot := names["_"]; hasRoot {
				log.Println("Root entry already exists in nostr.json")
				return nil
			}
		}
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(nostrJsonPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %s", err)
	}

	// Create nostr.json with relay pubkey as root
	nostrData := map[string]interface{}{
		"names": map[string]interface{}{
			"_": config.RelayPubkey,
		},
	}

	updatedData, err := json.MarshalIndent(nostrData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nostr.json: %s", err)
	}

	if err := os.WriteFile(nostrJsonPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write nostr.json: %s", err)
	}

	log.Printf("Initialized nostr.json with root entry: _ -> %s", config.RelayPubkey)
	return nil
}

// setupDashboardHandlers adds all the API endpoints for the dashboard
func setupDashboardHandlers(relay *khatru.Relay, config Config) {
	// Serve dashboard HTML
	relay.Router().HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.ServeFile(w, r, "./public/dashboard.html")
	})

	// API: Login endpoint
	relay.Router().HandleFunc("/api/dashboard/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Pubkey string `json:"pubkey"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Find the "_" user in nostr.json
		adminPubkey := ""
		for name, pk := range data.Names {
			if name == "_" {
				adminPubkey = pk
				break
			}
		}

		// Fallback to config.RelayPubkey if "_" not found (for safety/backward compatibility)
		if adminPubkey == "" {
			adminPubkey = config.RelayPubkey
		}

		// Validate pubkey against admin pubkey
		if req.Pubkey != adminPubkey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Access denied. Only the relay operator ('_' in nostr.json) can login."})
			return
		}

		// Set a simple session cookie for the UI endpoint
		http.SetCookie(w, &http.Cookie{
			Name:     "dashboard_session",
			Value:    req.Pubkey,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   3600, // 1 hour
		})

		// Return dashboard data
		response := map[string]interface{}{
			"relayName":        config.RelayName,
			"relayDescription": config.RelayDescription,
			"users":            data.Names,
			"environment":      getEnvironmentVars(),
			"isRemote":         config.NPUBDomain != "",
			"npubDomain":       config.NPUBDomain,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// API: Get dashboard UI
	relay.Router().HandleFunc("/api/dashboard/ui", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check session cookie
		cookie, err := r.Cookie("dashboard_session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Verify cookie value matches admin pubkey
		adminPubkey := ""
		for name, pk := range data.Names {
			if name == "_" {
				adminPubkey = pk
				break
			}
		}
		if adminPubkey == "" {
			adminPubkey = config.RelayPubkey
		}

		if cookie.Value != adminPubkey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		http.ServeFile(w, r, "./templates/dashboard_view.html")
	})

	// API: Logout endpoint
	relay.Router().HandleFunc("/api/dashboard/logout", func(w http.ResponseWriter, r *http.Request) {
		// Clear session cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "dashboard_session",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	})

	// API: Users endpoint (GET for list, POST for add)
	relay.Router().HandleFunc("/api/dashboard/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			response := map[string]interface{}{
				"users":      data.Names,
				"isRemote":   config.NPUBDomain != "",
				"npubDomain": config.NPUBDomain,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "POST":
			// Only allow if using local nostr.json
			if config.NPUBDomain != "" {
				http.Error(w, "Cannot modify users when using remote nostr.json", http.StatusForbidden)
				return
			}

			var req struct {
				Name   string `json:"name"`
				Pubkey string `json:"pubkey"`
			}

			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			// Validate inputs
			if req.Name == "" || req.Pubkey == "" {
				http.Error(w, "Name and pubkey are required", http.StatusBadRequest)
				return
			}

			// Validate pubkey format (64 hex chars)
			if len(req.Pubkey) != 64 || !isValidHex(req.Pubkey) {
				http.Error(w, "Invalid pubkey format", http.StatusBadRequest)
				return
			}

			// Add user to local nostr.json
			if err := addOrUpdateUser(req.Name, req.Pubkey); err != nil {
				http.Error(w, "Failed to add user: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Refresh data
			fetchNostrData("")

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
	})

	// API: Individual user operations (PUT for update, DELETE for delete)
	relay.Router().HandleFunc("/api/dashboard/user/", func(w http.ResponseWriter, r *http.Request) {
		// Only allow if using local nostr.json
		if config.NPUBDomain != "" {
			http.Error(w, "Cannot modify users when using remote nostr.json", http.StatusForbidden)
			return
		}

		// Extract pubkey from URL
		pubkey := strings.TrimPrefix(r.URL.Path, "/api/dashboard/user/")
		if pubkey == "" {
			http.Error(w, "Missing pubkey", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case "PUT":
			var req struct {
				Name string `json:"name"`
			}

			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			if req.Name == "" {
				http.Error(w, "Name is required", http.StatusBadRequest)
				return
			}

			// Update user in local nostr.json
			if err := addOrUpdateUser(req.Name, pubkey); err != nil {
				http.Error(w, "Failed to update user: "+err.Error(), http.StatusInternalServerError)
				return
			}

		case "DELETE":
			// Don't allow deleting the root entry
			if pubkey == config.RelayPubkey {
				http.Error(w, "Cannot delete root entry", http.StatusForbidden)
				return
			}

			// Delete user from local nostr.json
			if err := deleteUser(pubkey); err != nil {
				http.Error(w, "Failed to delete user: "+err.Error(), http.StatusInternalServerError)
				return
			}

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Refresh data
		fetchNostrData("")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	})

	// API: Get environment variables
	relay.Router().HandleFunc("/api/dashboard/environment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		response := map[string]interface{}{
			"environment": getEnvironmentVars(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// API: Convert pubkey
	relay.Router().HandleFunc("/api/dashboard/convert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Input string `json:"input"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		var result map[string]interface{}
		if strings.HasPrefix(req.Input, "npub1") {
			// Convert npub to hex
			hex, err := npubToHex(req.Input)
			if err != nil {
				result = map[string]interface{}{"error": "Invalid npub format: " + err.Error()}
			} else {
				result = map[string]interface{}{"hex": hex}
			}
		} else if len(req.Input) == 64 && isValidHex(req.Input) {
			// Convert hex to npub
			npub, err := hexToNpub(req.Input)
			if err != nil {
				result = map[string]interface{}{"error": "Invalid hex format: " + err.Error()}
			} else {
				result = map[string]interface{}{"npub": npub}
			}
		} else {
			result = map[string]interface{}{"error": "Invalid pubkey format. Expected 64-char hex or npub1..."}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
}

// getEnvironmentVars returns a map of environment variables (excluding sensitive ones)
func getEnvironmentVars() map[string]string {
	envVars := make(map[string]string)

	// List of environment variables to show (excluding sensitive ones)
	showVars := []string{
		"RELAY_NAME", "RELAY_PUBKEY", "RELAY_DESCRIPTION",
		"TEAM_DOMAIN", "NPUB_DOMAIN", "DB_ENGINE", "DB_PATH",
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB",
		"DATABASE_URL", "BLOSSOM_ENABLED", "BLOSSOM_PATH",
		"BLOSSOM_URL", "WEBSOCKET_URL", "ALLOWED_KINDS",
		"PUBLIC_ALLOWED_KINDS", "TRUSTED_CLIENT_NAME", "TRUSTED_CLIENT_KINDS",
		"MAX_UPLOAD_SIZE_MB", "RELAY_PORT", "ALLOWED_MIRROR_HOSTS",
		"STORAGE_BACKEND", "S3_ENDPOINT", "S3_BUCKET", "S3_REGION",
		"S3_PUBLIC_URL", "NIP05_PATH",
	}

	for _, varName := range showVars {
		value := os.Getenv(varName)
		if value != "" {
			// Mask sensitive values
			if strings.Contains(strings.ToLower(varName), "password") ||
				strings.Contains(strings.ToLower(varName), "secret") ||
				strings.Contains(strings.ToLower(varName), "key") {
				envVars[varName] = "***MASKED***"
			} else {
				envVars[varName] = value
			}
		} else {
			envVars[varName] = ""
		}
	}

	return envVars
}

// addOrUpdateUser adds or updates a user in the local nostr.json
func addOrUpdateUser(name, pubkey string) error {
	nostrJsonPath := "./public/.well-known/nostr.json"

	// Read existing file
	var nostrData map[string]interface{}
	if body, err := os.ReadFile(nostrJsonPath); err == nil {
		if err := json.Unmarshal(body, &nostrData); err != nil {
			return fmt.Errorf("failed to parse existing nostr.json: %s", err)
		}
	} else {
		// Create new structure if file doesn't exist
		nostrData = map[string]interface{}{
			"names": map[string]interface{}{},
		}
	}

	// Ensure names map exists
	names, ok := nostrData["names"].(map[string]interface{})
	if !ok {
		names = map[string]interface{}{}
		nostrData["names"] = names
	}

	// Add or update user
	names[name] = pubkey

	// Write back to file
	updatedData, err := json.MarshalIndent(nostrData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nostr.json: %s", err)
	}

	if err := os.WriteFile(nostrJsonPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write nostr.json: %s", err)
	}

	log.Printf("Added/updated user: %s -> %s", name, pubkey)
	return nil
}

// deleteUser removes a user from the local nostr.json
func deleteUser(pubkey string) error {
	nostrJsonPath := "./public/.well-known/nostr.json"

	// Read existing file
	var nostrData map[string]interface{}
	body, err := os.ReadFile(nostrJsonPath)
	if err != nil {
		return fmt.Errorf("failed to read nostr.json: %s", err)
	}

	if err := json.Unmarshal(body, &nostrData); err != nil {
		return fmt.Errorf("failed to parse nostr.json: %s", err)
	}

	// Get names map
	names, ok := nostrData["names"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid nostr.json structure")
	}

	// Find and remove user with matching pubkey
	var userToDelete string
	for name, pk := range names {
		if pk == pubkey {
			userToDelete = name
			break
		}
	}

	if userToDelete == "" {
		return fmt.Errorf("user not found")
	}

	delete(names, userToDelete)

	// Write back to file
	updatedData, err := json.MarshalIndent(nostrData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal nostr.json: %s", err)
	}

	if err := os.WriteFile(nostrJsonPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write nostr.json: %s", err)
	}

	log.Printf("Deleted user: %s", userToDelete)
	return nil
}

// npubToHex converts npub format to hex using bech32 decoding
func npubToHex(npub string) (string, error) {
	if len(npub) < 5 || !strings.HasPrefix(npub, "npub1") {
		return "", fmt.Errorf("invalid npub format: must start with npub1")
	}

	// Simple bech32 validation and decoding
	// This is a simplified implementation - in production you'd use a proper bech32 library
	const alphabet = "023456789acdefghjklmnpqrstuvwxyz"

	// Remove prefix and convert to lowercase
	encoded := strings.ToLower(npub[5:])

	// Validate characters
	for _, c := range encoded {
		if !strings.ContainsRune(alphabet, c) {
			return "", fmt.Errorf("invalid character in npub")
		}
	}

	// Convert to 5-bit groups
	var data []byte
	for _, c := range encoded {
		index := strings.IndexRune(alphabet, c)
		if index == -1 {
			return "", fmt.Errorf("invalid character in npub")
		}

		// Convert to 5 bits
		for i := 4; i >= 0; i-- {
			bit := byte((index >> uint(i)) & 1)
			data = appendBits(data, bit)
		}
	}

	// Remove checksum (last 6 bits = checksum)
	if len(data) < 6 {
		return "", fmt.Errorf("npub too short")
	}
	data = data[:len(data)-6]

	// Convert bits to bytes
	var bytes []byte
	for i := 0; i < len(data); i += 8 {
		if i+8 <= len(data) {
			var b byte
			for j := 0; j < 8; j++ {
				if i+j < len(data) {
					b |= data[i+j] << uint(7-j)
				}
			}
			bytes = append(bytes, b)
		}
	}

	if len(bytes) < 32 {
		return "", fmt.Errorf("decoded data too short")
	}

	return hex.EncodeToString(bytes[:32]), nil
}

// appendBits appends a bit to a byte slice
func appendBits(data []byte, bit byte) []byte {
	byteIndex := len(data) / 8
	bitIndex := len(data) % 8

	if bitIndex == 0 {
		data = append(data, 0)
	}

	data[byteIndex] |= bit << uint(7-bitIndex)
	return data
}

// hexToNpub converts hex to npub format using bech32 encoding
func hexToNpub(hexStr string) (string, error) {
	if len(hexStr) != 64 || !isValidHex(hexStr) {
		return "", fmt.Errorf("invalid hex format")
	}

	// Decode hex to bytes
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", fmt.Errorf("failed to decode hex: %s", err)
	}

	// Take first 32 bytes
	if len(decoded) > 32 {
		decoded = decoded[:32]
	}

	// Convert bytes to 5-bit groups
	var bits []byte
	for _, b := range decoded {
		for i := 7; i >= 0; i-- {
			bit := (b >> uint(i)) & 1
			bits = append(bits, bit)
		}
	}

	// Convert to base32
	const alphabet = "023456789acdefghjklmnpqrstuvwxyz"
	var encoded string
	for i := 0; i < len(bits); i += 5 {
		if i+5 <= len(bits) {
			value := byte(0)
			for j := 0; j < 5; j++ {
				if i+j < len(bits) {
					value |= bits[i+j] << uint(4-j)
				}
			}
			if int(value) < len(alphabet) {
				encoded += string(alphabet[value])
			}
		}
	}

	return "npub1" + encoded, nil
}
