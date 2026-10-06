package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func relayURL(t *testing.T) string {
	t.Helper()
	baseURL := strings.TrimRight(os.Getenv("E2E_RELAY_URL"), "/")
	if baseURL == "" {
		t.Skip("E2E_RELAY_URL is not set")
	}
	return baseURL
}

func websocketURL(baseURL string) string {
	if strings.HasPrefix(baseURL, "https://") {
		return "wss://" + strings.TrimPrefix(baseURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(baseURL, "http://")
}

func writeSecretKey(t *testing.T) string {
	t.Helper()
	if secretKey := os.Getenv("E2E_SECRET_KEY"); secretKey != "" {
		return secretKey
	}
	if os.Getenv("E2E_READ_ONLY") == "true" {
		t.Skip("write checks are disabled")
	}
	return nostr.GeneratePrivateKey()
}

func TestHealth(t *testing.T) {
	baseURL := relayURL(t)
	for _, test := range []struct {
		path   string
		status string
	}{
		{path: "/health/live", status: "live"},
		{path: "/health/ready", status: "ready"},
		{path: "/api/health", status: "ok"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response, err := http.Get(baseURL + test.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, body = %s", response.StatusCode, body)
			}
			var payload struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Status != test.status {
				t.Fatalf("status = %q, want %q", payload.Status, test.status)
			}
		})
	}
}

func TestRelayInformation(t *testing.T) {
	baseURL := relayURL(t)
	request, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/nostr+json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "application/nostr+json") {
		t.Fatalf("Content-Type = %q", contentType)
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name == "" {
		t.Fatal("relay name is empty")
	}
}

func TestEventLifecycle(t *testing.T) {
	baseURL := relayURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, websocketURL(baseURL))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	secretKey := writeSecretKey(t)
	publicKey, err := nostr.GetPublicKey(secretKey)
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{
		PubKey:    publicKey,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{{"client", "swarm-e2e"}},
		Content:   fmt.Sprintf("swarm-e2e-%d", time.Now().UnixNano()),
	}
	if err := event.Sign(secretKey); err != nil {
		t.Fatal(err)
	}
	if err := relay.Publish(ctx, event); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	events, err := relay.QuerySync(ctx, nostr.Filter{IDs: []string{event.ID}, Limit: 1})
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("query returned %d events", len(events))
	}

	deleteEvent := nostr.Event{
		PubKey:    publicKey,
		CreatedAt: nostr.Now(),
		Kind:      5,
		Tags:      nostr.Tags{{"e", event.ID}},
		Content:   "swarm e2e cleanup",
	}
	if err := deleteEvent.Sign(secretKey); err != nil {
		t.Fatal(err)
	}
	if err := relay.Publish(ctx, deleteEvent); err != nil {
		t.Fatalf("delete event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		events, err = relay.QuerySync(ctx, nostr.Filter{IDs: []string{event.ID}, Limit: 1})
		if err == nil && len(events) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("event remained queryable after deletion")
}

func TestRejectsUnauthorizedKind(t *testing.T) {
	baseURL := relayURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, websocketURL(baseURL))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	secretKey := writeSecretKey(t)
	publicKey, err := nostr.GetPublicKey(secretKey)
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{
		PubKey:    publicKey,
		CreatedAt: nostr.Now(),
		Kind:      30000,
		Content:   "swarm-e2e-rejected",
	}
	if err := event.Sign(secretKey); err != nil {
		t.Fatal(err)
	}
	if err := relay.Publish(ctx, event); err == nil {
		t.Fatal("unauthorized event kind was accepted")
	}
}
