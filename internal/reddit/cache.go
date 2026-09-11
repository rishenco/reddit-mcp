package reddit

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

// cache is a byte-bounded LRU over raw Reddit responses.
//
// It exists for the rate limit, not for latency: Reddit allows on the order of
// 100 requests per minute, and an agent working through a thread re-reads the
// same listing or post several times in a session. A nil *cache is a valid
// disabled cache, so callers never have to branch on it.
type cache struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	order    *list.List // front = most recently used
	items    map[string]*list.Element

	hits   int64
	misses int64
}

type cacheEntry struct {
	key         string
	body        []byte
	contentType string
	expiresAt   time.Time
}

func newCache(maxBytes int64) *cache {
	if maxBytes <= 0 {
		return nil
	}

	return &cache{
		maxBytes: maxBytes,
		order:    list.New(),
		items:    make(map[string]*list.Element),
	}
}

func (c *cache) get(key string) (body []byte, contentType string, ok bool) {
	if c == nil {
		return nil, "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, found := c.items[key]
	if !found {
		c.misses++

		return nil, "", false
	}

	entry, _ := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.removeElement(elem)

		c.misses++

		return nil, "", false
	}

	c.order.MoveToFront(elem)

	c.hits++

	return entry.body, entry.contentType, true
}

func (c *cache) put(key string, body []byte, contentType string, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}

	size := int64(len(body))
	if size > c.maxBytes {
		return // a single response larger than the whole budget is not worth evicting everything for
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, found := c.items[key]; found {
		c.removeElement(elem)
	}

	stored := make([]byte, len(body))

	copy(stored, body)

	elem := c.order.PushFront(&cacheEntry{
		key:         key,
		body:        stored,
		contentType: contentType,
		expiresAt:   time.Now().Add(ttl),
	})
	c.items[key] = elem

	c.used += size

	for c.used > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}

		c.removeElement(oldest)
	}
}

// removeElement drops an entry. The caller must hold c.mu.
func (c *cache) removeElement(elem *list.Element) {
	entry, _ := elem.Value.(*cacheEntry)

	c.order.Remove(elem)
	delete(c.items, entry.key)

	c.used -= int64(len(entry.body))
}

func (c *cache) stats() (hits, misses int64, entries int) {
	if c == nil {
		return 0, 0, 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.hits, c.misses, len(c.items)
}

// ttlFor picks how long a response stays fresh. Listings churn, a posted
// comment tree churns more slowly, and profile or subreddit metadata barely
// moves, so one configured base TTL is scaled per class rather than exposing
// three knobs nobody wants to tune.
func ttlFor(path string, base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}

	switch {
	case strings.HasSuffix(path, "/about"), strings.HasPrefix(path, "/user/"):
		return base * 3
	case strings.HasPrefix(path, "/comments/"), strings.HasPrefix(path, "/by_id/"):
		return base * 2
	default:
		return base
	}
}
