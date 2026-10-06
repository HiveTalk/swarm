package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func signedTestEvent(t *testing.T, secretKey string, kind int) nostr.Event {
	t.Helper()
	publicKey, err := nostr.GetPublicKey(secretKey)
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{
		PubKey:    publicKey,
		CreatedAt: nostr.Now(),
		Kind:      kind,
		Tags:      nostr.Tags{{"client", "swarm-e2e"}, {"d", fmt.Sprintf("%d", time.Now().UnixNano())}},
		Content:   fmt.Sprintf("swarm-e2e-kind-%d-%d", kind, time.Now().UnixNano()),
	}
	if err := event.Sign(secretKey); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestAccessPolicy(t *testing.T) {
	baseURL := relayURL(t)
	teamSecretKey := requiredSecretKey(t, "E2E_TEAM_SECRET_KEY")
	adminSecretKey := requiredSecretKey(t, "E2E_ADMIN_SECRET_KEY")
	publicKinds := configuredKinds(t, "E2E_PUBLIC_KINDS")
	teamKinds := configuredKinds(t, "E2E_TEAM_KINDS")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, websocketURL(baseURL))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	publicKindSet := make(map[int]bool, len(publicKinds))
	configuredKindSet := make(map[int]bool, len(publicKinds)+len(teamKinds))
	for _, kind := range publicKinds {
		publicKindSet[kind] = true
		configuredKindSet[kind] = true
		t.Run(fmt.Sprintf("public-kind-%d", kind), func(t *testing.T) {
			event := signedTestEvent(t, nostr.GeneratePrivateKey(), kind)
			if err := relay.Publish(ctx, event); err != nil {
				t.Fatalf("public kind %d rejected: %v", kind, err)
			}
		})
	}

	for _, kind := range teamKinds {
		configuredKindSet[kind] = true
		t.Run(fmt.Sprintf("team-kind-%d", kind), func(t *testing.T) {
			event := signedTestEvent(t, teamSecretKey, kind)
			if err := relay.Publish(ctx, event); err != nil {
				t.Fatalf("team kind %d rejected: %v", kind, err)
			}
		})
	}

	restrictedKind := -1
	for _, kind := range teamKinds {
		if !publicKindSet[kind] {
			restrictedKind = kind
			break
		}
	}
	if restrictedKind < 0 {
		t.Fatal("team kinds do not contain a restricted kind")
	}
	if err := relay.Publish(ctx, signedTestEvent(t, nostr.GeneratePrivateKey(), restrictedKind)); err == nil {
		t.Fatalf("non-team user published restricted kind %d", restrictedKind)
	}
	if err := relay.Publish(ctx, signedTestEvent(t, adminSecretKey, restrictedKind)); err != nil {
		t.Fatalf("admin could not publish restricted kind %d: %v", restrictedKind, err)
	}

	unconfiguredKind := 31999
	for configuredKindSet[unconfiguredKind] {
		unconfiguredKind++
	}
	if err := relay.Publish(ctx, signedTestEvent(t, teamSecretKey, unconfiguredKind)); err == nil {
		t.Fatalf("team user published unconfigured kind %d", unconfiguredKind)
	}
}

func TestDashboardAuthentication(t *testing.T) {
	baseURL := relayURL(t)
	adminSecretKey := requiredSecretKey(t, "E2E_ADMIN_SECRET_KEY")
	teamSecretKey := requiredSecretKey(t, "E2E_TEAM_SECRET_KEY")

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	login := func(secretKey string) (*http.Response, nostr.Event) {
		response, err := client.Get(baseURL + "/api/dashboard/challenge")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var challenge struct {
			Challenge string `json:"challenge"`
			Kind      int    `json:"kind"`
		}
		if err := json.NewDecoder(response.Body).Decode(&challenge); err != nil {
			t.Fatal(err)
		}
		event := signedTestEvent(t, secretKey, challenge.Kind)
		event.Tags = nostr.Tags{{"action", "dashboard-login"}}
		event.Content = challenge.Challenge
		if err := event.Sign(secretKey); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(map[string]interface{}{"event": event})
		if err != nil {
			t.Fatal(err)
		}
		response, err = client.Post(baseURL+"/api/dashboard/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response, event
	}

	response, event := login(adminSecretKey)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin login status = %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = client.Get(baseURL + "/api/dashboard/ui")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dashboard UI status = %d", response.StatusCode)
	}

	forgedJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	adminPubkey, err := nostr.GetPublicKey(adminSecretKey)
	if err != nil {
		t.Fatal(err)
	}
	forgedJar.SetCookies(parsedBaseURL, []*http.Cookie{{Name: "dashboard_session", Value: adminPubkey, Path: "/"}})
	forgedClient := &http.Client{Jar: forgedJar, Timeout: 10 * time.Second}
	response, err = forgedClient.Get(baseURL + "/api/dashboard/ui")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged session status = %d", response.StatusCode)
	}

	replayBody, err := json.Marshal(map[string]interface{}{"event": event})
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Post(baseURL+"/api/dashboard/login", "application/json", bytes.NewReader(replayBody))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed challenge status = %d", response.StatusCode)
	}

	response, _ = login(teamSecretKey)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-admin login status = %d", response.StatusCode)
	}
}
