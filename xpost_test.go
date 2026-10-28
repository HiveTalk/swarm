package main

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
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

	c := &Crossposter{}
	for _, test := range tests {
		result := c.cleanForX(test.input)
		if result != test.expected {
			t.Errorf("%s: cleanForX(%q) = %q, expected %q",
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

func TestMentionNameResolution(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatal(err)
	}
	npub, err := nip19.EncodePublicKey(pk)
	if err != nil {
		t.Fatal(err)
	}

	kind0 := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      0,
		Content:   `{"name":"junglekid","display_name":"Jungle Kids"}`,
	}
	queryFn := func(ctx context.Context, f nostr.Filter) (chan *nostr.Event, error) {
		ch := make(chan *nostr.Event, 1)
		if f.Kinds[0] == 0 && f.Authors[0] == pk {
			ch <- kind0
		}
		close(ch)
		return ch, nil
	}

	c := &Crossposter{queryFn: queryFn}

	t.Run("npub renders as display name", func(t *testing.T) {
		got := c.cleanForX("shoutout to nostr:" + npub + " for this")
		if !strings.HasPrefix(got, "shoutout to Jungle Kids for this") {
			t.Fatalf("expected display name, got %q", got)
		}
	})

	t.Run("unknown profile falls back to short npub", func(t *testing.T) {
		other, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
		otherNpub, _ := nip19.EncodePublicKey(other)
		got := c.cleanForX("hi nostr:" + otherNpub)
		if !strings.Contains(got, "hi "+otherNpub[:12]+"…") {
			t.Fatalf("expected short npub fallback, got %q", got)
		}
	})

	t.Run("team name used when no kind-0", func(t *testing.T) {
		other, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
		otherNpub, _ := nip19.EncodePublicKey(other)
		data.Names["teammate"] = other
		defer delete(data.Names, "teammate")
		got := c.cleanForX("hi nostr:" + otherNpub)
		if !strings.Contains(got, "hi teammate") {
			t.Fatalf("expected team name, got %q", got)
		}
	})

	t.Run("note links still become nostr.at", func(t *testing.T) {
		got := c.cleanForX("see nostr:note1qqx0z0z")
		if !strings.Contains(got, "https://nostr.at/note1qqx0z0z") {
			t.Fatalf("note ref should stay a link, got %q", got)
		}
	})
}

func TestTweetPayloadVideo(t *testing.T) {
	mkEvent := func(content string) *nostr.Event {
		return &nostr.Event{
			ID:        "0000000000000000000000000000000000000000000000000000000000000099",
			PubKey:    "0000000000000000000000000000000000000000000000000000000000000002",
			CreatedAt: nostr.Timestamp(1700000000),
			Kind:      1,
			Content:   content,
			Tags:      nostr.Tags{},
		}
	}

	t.Run("mp4 is extracted and stripped from textMedia", func(t *testing.T) {
		ev := mkEvent("watch this\n\nhttps://cdn.example.com/clip.mp4\n\nmore text")
		p := (&Crossposter{relayURL: ""}).buildTweetPayload(ev)
		if p.mediaURL != "https://cdn.example.com/clip.mp4" {
			t.Fatalf("mediaURL = %q", p.mediaURL)
		}
		if strings.Contains(p.textMedia, "clip.mp4") {
			t.Fatalf("textMedia should not contain the video URL: %q", p.textMedia)
		}
		if !strings.Contains(p.text, "clip.mp4") {
			t.Fatalf("text should keep the URL as fallback: %q", p.text)
		}
		if strings.Contains(p.textMedia, "\n\n\n") {
			t.Fatalf("stripped text has blank-line collapse issue: %q", p.textMedia)
		}
	})

	t.Run("video URL with query string", func(t *testing.T) {
		ev := mkEvent("vid https://cdn.example.com/clip.mp4?x=1&y=2 end")
		p := (&Crossposter{}).buildTweetPayload(ev)
		if !strings.HasPrefix(p.mediaURL, "https://cdn.example.com/clip.mp4") {
			t.Fatalf("mediaURL = %q", p.mediaURL)
		}
	})

	t.Run("no video means empty media fields", func(t *testing.T) {
		ev := mkEvent("just text https://example.com/page")
		p := (&Crossposter{}).buildTweetPayload(ev)
		if p.mediaURL != "" || p.textMedia != "" {
			t.Fatalf("unexpected media fields: %+v", p)
		}
	})
}
