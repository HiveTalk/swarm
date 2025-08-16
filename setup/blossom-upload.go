package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"golang.org/x/term"
)

type BlossomUploadResponse struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
	Uploaded int64  `json:"uploaded"`
}

func readPassword() string {
	fmt.Print("type your secret key as ncryptsec, nsec or hex: ")
	
	// Read password without echoing to terminal
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		fmt.Printf("\nError reading password: %v\n", err)
		os.Exit(1)
	}
	fmt.Println() // Print newline after password input
	
	return string(bytePassword)
}

func main() {
	var promptSec = flag.Bool("prompt-sec", false, "prompt the user to paste a hex or nsec with which to sign the event")
	var verbose = flag.Bool("verbose", false, "enable verbose debugging output")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Println("Usage: go run blossom-upload.go [--prompt-sec] <server-url> <file-path>")
		fmt.Println("Example: go run blossom-upload.go --prompt-sec https://swarm.hivetalk.org ~/Desktop/video.mp4")
		fmt.Println("Example: go run blossom-upload.go https://swarm.hivetalk.org ~/Desktop/video.mp4 nsec1...")
		os.Exit(1)
	}

	serverURL := args[0]
	filePath := args[1]
	
	var privateKey string
	if *promptSec {
		privateKey = readPassword()
	} else if len(args) >= 3 {
		privateKey = args[2]
	} else {
		fmt.Print("Enter your private key (nsec/hex): ")
		fmt.Scanln(&privateKey)
	}

	// Parse private key
	var sk string
	if _, data, err := nip19.Decode(privateKey); err == nil {
		sk = hex.EncodeToString(data.([]byte))
	} else {
		sk = privateKey
	}

	// Read file
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		fmt.Printf("Error getting file info: %v\n", err)
		os.Exit(1)
	}

	fileData, err := io.ReadAll(file)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Calculate SHA256
	hash := sha256.Sum256(fileData)
	sha256Hash := hex.EncodeToString(hash[:])

	// Get file extension and MIME type
	ext := filepath.Ext(filePath)
	_ = getMimeType(ext) // Use the mime type (suppress unused warning)

	fmt.Printf("📁 File: %s\n", filepath.Base(filePath))
	fmt.Printf("📏 Size: %d bytes (%.2f MB)\n", fileInfo.Size(), float64(fileInfo.Size())/1024/1024)
	fmt.Printf("🔐 SHA256: %s\n", sha256Hash)
	fmt.Printf("📤 Uploading to: %s\n", serverURL)

	// Create authorization event (BUD-02 spec)
	pubkey, _ := nostr.GetPublicKey(sk)
	authEvent := &nostr.Event{
		Kind:      24242,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"t", "upload"},
			{"x", sha256Hash},
			{"expiration", strconv.FormatInt(nostr.Now().Time().Add(10*time.Minute).Unix(), 10)},
		},
		Content: "", // Empty content per BUD-02 spec
		PubKey:  pubkey,
	}

	// Sign the event
	if err := authEvent.Sign(sk); err != nil {
		fmt.Printf("Error signing event: %v\n", err)
		os.Exit(1)
	}

	// Create HTTP request with file data directly (not multipart)
	client := &http.Client{
		Timeout: 20 * time.Minute, // 20-minute timeout for very large files
	}

	uploadURL := serverURL + "/upload"
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(fileData))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		os.Exit(1)
	}

	// Set headers
	req.Header.Set("Content-Type", getMimeType(ext))
	req.Header.Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	req.Header.Set("User-Agent", "blossom-upload-cli/1.0")
	
	// Add authorization as header (BUD-02 format)
	authJSON, _ := json.Marshal(authEvent)
	req.Header.Set("Authorization", "Nostr "+string(authJSON))

	if *verbose {
		fmt.Printf("🔍 Debug Info:\n")
		fmt.Printf("  URL: %s\n", uploadURL)
		fmt.Printf("  Method: %s\n", req.Method)
		fmt.Printf("  Content-Type: %s\n", req.Header.Get("Content-Type"))
		fmt.Printf("  Content-Length: %s\n", req.Header.Get("Content-Length"))
		fmt.Printf("  Auth Event: %s\n", string(authJSON))
		fmt.Printf("  File SHA256: %s\n", sha256Hash)
	}

	fmt.Printf("⏳ Starting upload...\n")
	start := time.Now()

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error making request: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	duration := time.Since(start)
	fmt.Printf("⏱️  Upload completed in: %v\n", duration)

	if *verbose {
		fmt.Printf("🔍 Response Info:\n")
		fmt.Printf("  Status: %d %s\n", resp.StatusCode, resp.Status)
		fmt.Printf("  Headers:\n")
		for k, v := range resp.Header {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("  Response Body: %s\n", string(respBody))
	}

	if resp.StatusCode != 200 {
		fmt.Printf("❌ Upload failed with status %d: %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	// Parse response
	var uploadResp BlossomUploadResponse
	if err := json.Unmarshal(respBody, &uploadResp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		fmt.Printf("Raw response: %s\n", string(respBody))
		os.Exit(1)
	}

	// Success!
	fmt.Printf("✅ Upload successful!\n")
	fmt.Printf("🔗 URL: %s\n", uploadResp.URL)
	fmt.Printf("📊 Size: %d bytes\n", uploadResp.Size)
	fmt.Printf("📄 Type: %s\n", uploadResp.Type)
	fmt.Printf("⏰ Uploaded: %d\n", uploadResp.Uploaded)
}

func getMimeType(ext string) string {
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}
