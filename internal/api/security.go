package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func sanitizeContent(content string) string {
	var b strings.Builder
	b.Grow(len(content))
	for _, r := range content {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		if (r >= 0x200B && r <= 0x200F) || r == 0xFEFF || (r >= 0x2060 && r <= 0x2064) {
			continue
		}
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validateMediaPath(path, baseDir string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("media path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid media path: %w", err)
	}
	cleaned := filepath.Clean(abs)
	baseCleaned := filepath.Clean(baseDir) + string(os.PathSeparator)
	if !strings.HasPrefix(cleaned, baseCleaned) && cleaned != filepath.Clean(baseDir) {
		return "", fmt.Errorf("media path %q is outside allowed directory %s", path, baseDir)
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("media file not found: %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("media path is a directory, not a file: %s", path)
	}
	return cleaned, nil
}

type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int
	interval time.Duration
}

type bucket struct {
	tokens   int
	lastSeen time.Time
}

func newRateLimiter(rate int, interval time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     rate,
		interval: interval,
	}
}

func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.rate - 1, lastSeen: now}
		return true
	}
	elapsed := now.Sub(b.lastSeen)
	refill := int(elapsed / rl.interval)
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.rate {
			b.tokens = rl.rate
		}
		b.lastSeen = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}
