package main

import (
	"testing"
)

func TestGetS3ConfigFromEnv(t *testing.T) {
	// Test with missing env vars - should return nil
	cfg := getS3ConfigFromEnv()
	if cfg != nil {
		t.Error("Expected nil config when env vars are missing")
	}
}

func TestIsValidHex(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"abcdef0123456789", true},
		{"ABCDEF0123456789", true},
		{"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", true},
		{"ghijkl", false},
		{"abc!def", false},
		{"", true}, // empty string has no invalid chars
	}

	for _, test := range tests {
		result := isValidHex(test.input)
		if result != test.expected {
			t.Errorf("isValidHex(%q) = %v, expected %v", test.input, result, test.expected)
		}
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string
	}{
		{
			name:     "PNG signature",
			data:     append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 504)...),
			expected: "image/png",
		},
		{
			name:     "JPEG signature",
			data:     append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 508)...),
			expected: "image/jpeg",
		},
		{
			name:     "GIF signature",
			data:     append([]byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, make([]byte, 506)...),
			expected: "image/gif",
		},
		{
			name:     "Unknown/binary",
			data:     make([]byte, 512),
			expected: "application/octet-stream",
		},
		{
			name:     "Too short",
			data:     []byte{0x89, 0x50},
			expected: "application/octet-stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := detectContentType(test.data)
			if result != test.expected {
				t.Errorf("detectContentType() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestGetContentType(t *testing.T) {
	// Test PDF signature
	pdfHeader := append([]byte{0x25, 0x50, 0x44, 0x46}, make([]byte, 508)...)
	if ct := getContentType(pdfHeader); ct != "application/pdf" {
		t.Errorf("Expected application/pdf, got %s", ct)
	}

	// Test WebM signature
	webmHeader := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 508)...)
	if ct := getContentType(webmHeader); ct != "video/webm" {
		t.Errorf("Expected video/webm, got %s", ct)
	}
}
