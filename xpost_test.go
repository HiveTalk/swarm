package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nbd-wtf/go-nostr"
)

func TestCleanNostrLinks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no nostr links",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "npub link",
			input:    "shoutout to nostr:npub1abc0 for this",
			expected: "shoutout to https://nostr.at/npub1abc0 for this",
		},
		{
			name:     "nevent link",
			input:    "see nostr:nevent1qqsyx0kg",
			expected: "see https://nostr.at/nevent1qqsyx0kg",
		},
		{
			name:     "naddr link",
			input:    "read nostr:naddr1qqxnzdeev9",
			expected: "read https://nostr.at/naddr1qqxnzdeev9",
		},
		{
			name:     "nprofile link",
			input:    "ping nostr:nprofile1qqsv3zc",
			expected: "ping https://nostr.at/nprofile1qqsv3zc",
		},
		{
			name:     "multiple links",
			input:    "nostr:npub1aaa and nostr:note1zzz",
			expected: "https://nostr.at/npub1aaa and https://nostr.at/note1zzz",
		},
		{
			name:     "nsec is redacted",
			input:    "my key nostr:nsec1abcdef secret",
			expected: "my key [redacted] secret",
		},
		{
			name:     "not a nostr uri",
			input:    "no-stir:npub1abc stays",
			expected: "no-stir:npub1abc stays",
		},
		{
			name:     "bech32 charset stops at invalid chars",
			input:    "nostr:npub1abc!extra",
			expected: "https://nostr.at/npub1abc!extra",
		},
	}

	for _, test := range tests {
		result := cleanNostrLinks(test.input)
		if result != test.expected {
			t.Errorf("%s: cleanNostrLinks(%q) = %q, expected %q",
				test.name, test.input, result, test.expected)
		}
	}
}

func TestWeightedTweetLen(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"plain text", "hello", 5},
		{"empty", "", 0},
		{"one url", "check https://example.com/very/long/path now", 6 + 23 + 4},
		{"two urls", "https://a.co https://b.co", 47},
		{"multibyte", "héllo wörld", 11},
	}

	for _, test := range tests {
		result := weightedTweetLen(test.input)
		if result != test.expected {
			t.Errorf("%s: weightedTweetLen(%q) = %d, expected %d",
				test.name, test.input, result, test.expected)
		}
	}
}

func TestBuildTweetText(t *testing.T) {
	mkEvent := func(content string) *nostr.Event {
		return &nostr.Event{
			ID:        "0000000000000000000000000000000000000000000000000000000000000001",
			PubKey:    "0000000000000000000000000000000000000000000000000000000000000002",
			CreatedAt: nostr.Timestamp(1700000000),
			Kind:      1,
			Content:   content,
			Tags:      nostr.Tags{},
		}
	}

	t.Run("short note keeps full text and appends nostr.at link", func(t *testing.T) {
		ev := mkEvent("gm nostr")
		got := buildTweetText(ev, "wss://relay.example.com")
		if !strings.HasPrefix(got, "gm nostr\n\n") {
			t.Fatalf("expected text prefix, got %q", got)
		}
		if !strings.Contains(got, "https://nostr.at/nevent1") {
			t.Fatalf("expected nostr.at nevent link, got %q", got)
		}
	})

	t.Run("nostr links become nostr.at links", func(t *testing.T) {
		ev := mkEvent("thanks nostr:npub1abc0 for the zap")
		got := buildTweetText(ev, "")
		if !strings.Contains(got, "https://nostr.at/npub1abc0") {
			t.Fatalf("expected inline nostr.at npub link, got %q", got)
		}
		if strings.Contains(got, "nostr:") {
			t.Fatalf("no raw nostr: uri should remain, got %q", got)
		}
	})

	t.Run("nsec never reaches the tweet", func(t *testing.T) {
		ev := mkEvent("my secret nostr:nsec1abcdef oops")
		got := buildTweetText(ev, "")
		if strings.Contains(got, "nsec1") {
			t.Fatalf("nsec leaked into tweet: %q", got)
		}
	})

	t.Run("long note truncates with link preserved", func(t *testing.T) {
		long := strings.Repeat("word ", 200) // 1000 chars
		ev := mkEvent(long)
		got := buildTweetText(ev, "")
		if !strings.Contains(got, "https://nostr.at/nevent1") {
			t.Fatalf("footer link missing: %q", got)
		}
		if !strings.Contains(got, "…") {
			t.Fatalf("expected ellipsis in truncated tweet")
		}
		if w := weightedTweetLen(got); w > xMaxWeightedLen {
			t.Fatalf("tweet exceeds limit: weighted %d > %d", w, xMaxWeightedLen)
		}
	})

	t.Run("note with many urls truncates by weighted count", func(t *testing.T) {
		content := strings.Repeat("https://example.com/x ", 20) + strings.Repeat("tail ", 100)
		ev := mkEvent(content)
		got := buildTweetText(ev, "")
		if w := weightedTweetLen(got); w > xMaxWeightedLen {
			t.Fatalf("tweet exceeds limit: weighted %d > %d", w, xMaxWeightedLen)
		}
	})

	t.Run("empty content produces just the link", func(t *testing.T) {
		ev := mkEvent("   ")
		got := buildTweetText(ev, "")
		if !strings.HasPrefix(got, "\n\nhttps://nostr.at/") {
			t.Fatalf("expected bare link, got %q", got)
		}
	})
}

func TestTruncateToWeighted(t *testing.T) {
	t.Run("no-op when under budget", func(t *testing.T) {
		if got := truncateToWeighted("short text", 50); got != "short text" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("cuts at word boundary", func(t *testing.T) {
		got := truncateToWeighted("alpha beta gamma delta", 14)
		if got == "" || utf8.RuneCountInString(got) > 14 {
			t.Fatalf("bad truncation: %q", got)
		}
		if strings.HasSuffix(got, " ") {
			t.Fatalf("trailing space: %q", got)
		}
	})
	t.Run("urls count as 23", func(t *testing.T) {
		got := truncateToWeighted("hi https://example.com/loooooooooong there", 27)
		// "hi " (3) + URL (23) = 26 fits; " there" doesn't
		if !strings.Contains(got, "https://example.com/loooooooooong") {
			t.Fatalf("URL should be kept whole: %q", got)
		}
	})
}
