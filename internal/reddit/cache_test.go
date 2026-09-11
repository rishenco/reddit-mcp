package reddit

import (
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	c := newCache(1 << 20)

	c.put("k", []byte("value"), "json", time.Minute)

	body, contentType, ok := c.get("k")
	if !ok {
		t.Fatal("expected a hit")
	}

	if string(body) != "value" || contentType != "json" {
		t.Errorf("got %q/%q", body, contentType)
	}

	if _, _, ok := c.get("missing"); ok {
		t.Error("unexpected hit for an unknown key")
	}
}

func TestCacheExpires(t *testing.T) {
	c := newCache(1 << 20)

	c.put("k", []byte("value"), "json", time.Nanosecond)
	time.Sleep(time.Millisecond)

	if _, _, ok := c.get("k"); ok {
		t.Error("expired entry should miss")
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newCache(20)

	c.put("a", []byte("0123456789"), "json", time.Minute)
	c.put("b", []byte("0123456789"), "json", time.Minute)

	// Touching "a" makes "b" the eviction candidate.
	if _, _, ok := c.get("a"); !ok {
		t.Fatal("a should still be cached")
	}

	c.put("c", []byte("0123456789"), "json", time.Minute)

	if _, _, ok := c.get("b"); ok {
		t.Error("b was least recently used and should have been evicted")
	}

	if _, _, ok := c.get("a"); !ok {
		t.Error("a was used most recently and should have survived")
	}
}

func TestCacheRejectsOversizedEntry(t *testing.T) {
	c := newCache(10)

	c.put("big", make([]byte, 100), "json", time.Minute)

	if _, _, ok := c.get("big"); ok {
		t.Error("an entry larger than the whole budget should not be stored")
	}

	if c.used != 0 {
		t.Errorf("used = %d, want 0", c.used)
	}
}

func TestCacheOverwriteKeepsAccountingStraight(t *testing.T) {
	c := newCache(1 << 20)

	c.put("k", make([]byte, 100), "json", time.Minute)
	c.put("k", make([]byte, 10), "json", time.Minute)

	if c.used != 10 {
		t.Errorf("used = %d, want 10 after overwrite", c.used)
	}

	if _, _, entries := c.stats(); entries != 1 {
		t.Errorf("entries = %d, want 1", entries)
	}
}

func TestNilCacheIsDisabled(t *testing.T) {
	var c *cache

	c.put("k", []byte("v"), "json", time.Minute)

	if _, _, ok := c.get("k"); ok {
		t.Error("a nil cache must never hit")
	}

	if hits, misses, entries := c.stats(); hits != 0 || misses != 0 || entries != 0 {
		t.Error("a nil cache has no stats")
	}

	if newCache(0) != nil {
		t.Error("a zero budget disables the cache")
	}
}

func TestTTLScalesByPathClass(t *testing.T) {
	base := time.Minute

	if got := ttlFor("/r/golang/hot", base); got != base {
		t.Errorf("listing ttl = %v, want %v", got, base)
	}

	if got := ttlFor("/comments/abc", base); got != 2*base {
		t.Errorf("thread ttl = %v, want %v", got, 2*base)
	}

	if got := ttlFor("/r/golang/about", base); got != 3*base {
		t.Errorf("about ttl = %v, want %v", got, 3*base)
	}

	if got := ttlFor("/r/golang/hot", 0); got != 0 {
		t.Errorf("ttl = %v, want 0 when caching is off", got)
	}
}
