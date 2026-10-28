package xclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicIP(t *testing.T) {
	for _, tc := range []struct {
		ip     string
		public bool
	}{
		{"93.184.216.34", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"::ffff:127.0.0.1", false},
		{"169.254.169.254", false},
		{"10.0.0.1", false},
		{"224.0.1.1", false},
		{"100.64.0.1", false},
		{"198.18.0.1", false},
		{"192.0.2.1", false},
		{"2001:db8::1", false},
		{"2002:7f00:1::1", false},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := isPublicIP(net.ParseIP(tc.ip)); got != tc.public {
				t.Fatalf("isPublicIP(%s) = %t, want %t", tc.ip, got, tc.public)
			}
		})
	}
}

func TestPublicHTTPClientPinsValidatedIP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "video")
	}))
	defer upstream.Close()

	var lookups atomic.Int32
	lookup := func(_ context.Context, host string) ([]net.IP, error) {
		if host != "media.example" {
			return nil, fmt.Errorf("unexpected lookup: %s", host)
		}
		if lookups.Add(1) == 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || host != "93.184.216.34" {
			return nil, fmt.Errorf("dialed unvalidated address %q: %w", addr, err)
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	client := publicHTTPClient(5*time.Second, lookup, dial)
	resp, err := client.Get("http://media.example/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || lookups.Load() != 1 {
		t.Fatalf("status %d; DNS lookups %d, want 1", resp.StatusCode, lookups.Load())
	}
}

func TestPublicHTTPClientBlocksRedirectToLoopback(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/private", http.StatusFound)
	}))
	defer source.Close()

	lookup := func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || host != "93.184.216.34" {
			return nil, fmt.Errorf("dialed unvalidated address %q: %w", addr, err)
		}
		return (&net.Dialer{}).DialContext(ctx, network, source.Listener.Addr().String())
	}
	client := publicHTTPClient(5*time.Second, lookup, dial)
	_, err := client.Get("http://media.example/video.mp4")
	if err == nil || !strings.Contains(err.Error(), "SSRF guard") {
		t.Fatalf("expected SSRF guard error on redirect, got %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target received a request")
	}
}

func TestPublicHTTPClientAllowsPublicRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "video")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://cdn.example/video.mp4", http.StatusFound)
	}))
	defer source.Close()

	var lookups atomic.Int32
	lookup := func(_ context.Context, host string) ([]net.IP, error) {
		lookups.Add(1)
		switch host {
		case "media.example":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "cdn.example":
			return []net.IP{net.ParseIP("1.1.1.1")}, nil
		default:
			return nil, fmt.Errorf("unexpected lookup: %s", host)
		}
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		switch host {
		case "93.184.216.34":
			return (&net.Dialer{}).DialContext(ctx, network, source.Listener.Addr().String())
		case "1.1.1.1":
			return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
		default:
			return nil, fmt.Errorf("dialed unvalidated address %q", addr)
		}
	}
	client := publicHTTPClient(5*time.Second, lookup, dial)
	resp, err := client.Get("http://media.example/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "video" || lookups.Load() != 2 {
		t.Fatalf("body %q; DNS lookups %d, want 2", body, lookups.Load())
	}
}

func TestResolvePublicIPRejectsMixedDNSAnswers(t *testing.T) {
	lookup := func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}, nil
	}
	if _, err := resolvePublicIP(context.Background(), "media.example", lookup); err == nil {
		t.Fatal("mixed public/private DNS answer was accepted")
	}
}

func TestUploadMediaRejectsPrivateURL(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/video.mp4",
		"http://169.254.169.254/latest/meta-data",
		"http://[::ffff:127.0.0.1]/video.mp4",
		"file:///etc/passwd",
	} {
		if _, err := New("key", "secret").UploadMedia(context.Background(), "", "", raw); err == nil {
			t.Errorf("UploadMedia accepted %s", raw)
		}
	}
}
