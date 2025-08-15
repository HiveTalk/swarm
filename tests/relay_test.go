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

func testRelay() {
	// Test with the provided approved pubkey
	approvedNpub := "npub128jtgey22jdx90f7vecpy2unrn4usu3mcrlhaqpjlcy8kq8t8k7sldgax3"
	
	// Decode the npub to get the hex pubkey
	_, pubkeyInterface, err := nip19.Decode(approvedNpub)
	if err != nil {
		log.Fatalf("Failed to decode npub: %v", err)
	}
	approvedPubkey := pubkeyInterface.(string)
	
	fmt.Printf("Testing with approved pubkey: %s\n", approvedPubkey)
	
	// Test connection and subscription capabilities
	testConnectionReadOnly("wss://swarm.hivetalk.org", approvedPubkey)
	
	// Also test with a random key to see rejection behavior
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("Testing with random (non-approved) pubkey for comparison...")
	testPrivateKey := nostr.GeneratePrivateKey()
	testPublicKey, _ := nostr.GetPublicKey(testPrivateKey)
	
	fmt.Printf("Testing with random pubkey: %s\n", testPublicKey)
	testConnectionWithWrite("wss://swarm.hivetalk.org", testPrivateKey, testPublicKey)
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
			content := event.Content
			if len(content) > 50 {
				content = content[:50] + "..."
			}
			fmt.Printf("📨 Received event %d: %s\n", eventCount, content)
		case <-timeout:
			fmt.Printf("⏰ Subscription test completed. Received %d events\n", eventCount)
			return
		case <-ctx.Done():
			return
		}
	}
}

func testConnectionWithWrite(relayURL string, privateKey string, publicKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		log.Printf("❌ Failed to connect to %s: %v", relayURL, err)
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
		log.Printf("❌ Failed to sign event: %v", err)
		return
	}

	// Try to publish the event
	fmt.Printf("Attempting to publish event with pubkey %s...\n", publicKey[:8])
	
	err = relay.Publish(ctx, *event)
	if err != nil {
		fmt.Printf("❌ Event rejected by %s: %v\n", relayURL, err)
	} else {
		fmt.Printf("✅ Event accepted by %s\n", relayURL)
	}

	// Try to subscribe and see if we get any events back
	fmt.Printf("Checking if event was stored...\n")
	
	filters := []nostr.Filter{{
		Authors: []string{publicKey},
		Kinds:   []int{1},
		Limit:   1,
	}}

	sub, err := relay.Subscribe(ctx, filters)
	if err != nil {
		log.Printf("❌ Failed to subscribe: %v", err)
		return
	}

	// Listen for events for a short time
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-sub.Events:
			fmt.Printf("📨 Found stored event: %s\n", event.Content)
			return
		case <-timeout:
			fmt.Printf("⏰ No stored events found for this pubkey\n")
			return
		case <-ctx.Done():
			return
		}
	}
}
