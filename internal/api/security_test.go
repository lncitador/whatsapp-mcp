package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizeContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"with newlines", "line1\nline2", "line1\nline2"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeContent(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateMediaPath(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "media")
	os.MkdirAll(allowed, 0755)
	good := filepath.Join(allowed, "photo.jpg")
	os.WriteFile(good, []byte("fake"), 0644)

	t.Run("valid path", func(t *testing.T) {
		resolved, err := validateMediaPath(good, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resolved != good {
			t.Fatalf("got %q, want %q", resolved, good)
		}
	})

	t.Run("path traversal blocked", func(t *testing.T) {
		_, err := validateMediaPath("../../etc/passwd", dir)
		if err == nil {
			t.Fatal("want error for path traversal")
		}
	})

	t.Run("outside base dir blocked", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "secret.txt")
		os.WriteFile(other, []byte("secret"), 0644)
		_, err := validateMediaPath(other, dir)
		if err == nil {
			t.Fatal("want error for path outside base")
		}
	})

	t.Run("nonexistent file blocked", func(t *testing.T) {
		_, err := validateMediaPath(filepath.Join(allowed, "nope.jpg"), dir)
		if err == nil {
			t.Fatal("want error for missing file")
		}
	})
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(2, time.Second)
	key := "test"

	if !rl.Allow(key) {
		t.Fatal("first call should be allowed")
	}
	if !rl.Allow(key) {
		t.Fatal("second call should be allowed")
	}
	if rl.Allow(key) {
		t.Fatal("third call should be blocked")
	}
}
