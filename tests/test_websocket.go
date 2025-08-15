package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func main() {
	// Test with the provided approved pubkey
	approvedNpub := "npub128jtgey22jdx90f7vecpy2unrn4usu3mcrlhaqpjlcy8kq8t8k7sldgax3"
	
	// Decode the npub to get the hex pubkey
	_, pubkeyInterface, err := nip19.Decode(approvedNpub)
	if err != nil {
		log.Fatalf("Failed to decode npub: %v", err)
	}
	approvedPubkey := pubkeyInterface.(string)
	
	fmt.Printf("Testing with approved pubkey: %s\n", approvedPubkey)
	
	// Since we don't have the private key for this approved pubkey, 
	// we'll test connection and subscription capabilities
	testConnectionReadOnly("wss://swarm.hivetalk.org", approvedPubkey)
	
	// Also test with a random key to see rejection behavior
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("Testing with random (non-approved) pubkey for comparison...")
	testPrivateKey := nostr.GeneratePrivateKey()
	testPublicKey, _ := nostr.GetPublicKey(testPrivateKey)
	
	fmt.Printf("Testing with random pubkey: %s\n", testPublicKey)
	testConnection("wss://swarm.hivetalk.org", testPrivateKey, testPublicKey)
}

func testConnectionReadOnly(relayURL string, publicKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		log.Printf("❌ Failed to connect to %s: %v", relayURL, err)
		return
	}
	defer relay.Close()

	fmt.Printf("✅ Successfully connected to %s\n", relayURL)

	// Try to subscribe to events from this pubkey
	fmt.Printf("Attempting to subscribe to events from pubkey %s...\n", publicKey[:8])
	
	filters := []nostr.Filter{{
		Authors: []string{publicKey},
		Kinds:   []int{1},
		Limit:   5,
	}}

	sub, err := relay.Subscribe(ctx, filters)
	if err != nil {
		log.Printf("❌ Failed to subscribe: %v", err)
		return
	}

	fmt.Printf("✅ Successfully subscribed to %s\n", relayURL)

	// Listen for events for a short time
	timeout := time.After(3 * time.Second)
	eventCount := 0
	for {
		select {
		case event := <-sub.Events:
			eventCount++
			fmt.Printf("📨 Received event %d: %s\n", eventCount, event.Content[:min(50, len(event.Content))])
		case <-timeout:
			fmt.Printf("⏰ Subscription test completed. Received %d events\n", eventCount)
			return
		case <-ctx.Done():
			return
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func testConnection(relayURL string, privateKey string, publicKey string, isLocalhost bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		log.Printf("Failed to connect to %s: %v", relayURL, err)
		return
	}
	defer relay.Close()

	fmt.Printf("✅ Successfully connected to %s\n", relayURL)

	// Create a test event
	event := &nostr.Event{
		Kind:      1, // Text note
		Content:   fmt.Sprintf("Test message from %s at %s", publicKey[:8], time.Now().Format(time.RFC3339)),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
		PubKey:    publicKey,
	}

	// Sign the event
	err = event.Sign(privateKey)
	if err != nil {
		log.Printf("Failed to sign event: %v", err)
		return
	}

	// Try to publish the event
	fmt.Printf("Attempting to publish event with pubkey %s...\n", publicKey[:8])
	
	status := relay.Publish(ctx, *event)
	
	select {
	case result := <-status:
		switch result {
		case nostr.PublishStatusSent:
			fmt.Printf("✅ Event sent successfully to %s\n", relayURL)
		case nostr.PublishStatusFailed:
			fmt.Printf("❌ Event failed to publish to %s\n", relayURL)
		case nostr.PublishStatusSucceeded:
			fmt.Printf("✅ Event published and accepted by %s\n", relayURL)
		}
	case <-ctx.Done():
		fmt.Printf("⏰ Timeout waiting for publish result from %s\n", relayURL)
	}

	// Try to subscribe and see if we get any rejection messages
	fmt.Printf("Listening for any response messages...\n")
	
	// Create a subscription to see server responses
	filters := []nostr.Filter{{
		Authors: []string{publicKey},
		Kinds:   []int{1},
		Limit:   1,
	}}

	sub, err := relay.Subscribe(ctx, filters)
	if err != nil {
		log.Printf("Failed to subscribe: %v", err)
		return
	}

	// Listen for events or errors for a short time
	timeout := time.After(3 * time.Second)
	for {
		select {
		case event := <-sub.Events:
			fmt.Printf("📨 Received event: %s\n", event.Content)
		case <-timeout:
			fmt.Printf("⏰ No events received within timeout\n")
			return
		case <-ctx.Done():
			return
		}
	}
}

// Helper function to test with a specific private key if you have one
func testWithSpecificKey(nsec string) {
	if nsec == "" {
		fmt.Println("No specific key provided, skipping...")
		return
	}
	
	_, privateKey, err := nip19.Decode(nsec)
	if err != nil {
		log.Printf("Failed to decode nsec: %v", err)
		return
	}
	
	publicKey, err := nostr.GetPublicKey(privateKey.(string))
	if err != nil {
		log.Printf("Failed to get public key: %v", err)
		return
	}
	
	fmt.Printf("Testing with provided key: %s\n", publicKey)
	testConnection("wss://swarm.hivetalk.org", privateKey.(string), publicKey, false)
}
