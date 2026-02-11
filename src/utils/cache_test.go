package utils

import (
	"testing"
	"time"
)

func TestMemoryCacheInitialization(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 10*time.Minute)

	if cache == nil {
		t.Fatal("Memory cache should not be nil")
	}

	if cache.data == nil {
		t.Fatal("Cache data should be initialized")
	}

	if cache.accessOrder == nil {
		t.Fatal("Cache access order should be initialized")
	}

	if cache.maxEntries != 100 {
		t.Errorf("Expected maxEntries to be 100, got %d", cache.maxEntries)
	}

	if cache.maxSize != 1024*1024 {
		t.Errorf("Expected maxSize to be 1MB, got %d", cache.maxSize)
	}
}

func TestMemoryCacheSetAndGet(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 10*time.Minute)

	testData := []byte("test data")
	testContentType := "application/json"
	testHeaders := map[string]string{
		"X-Custom": "value",
	}

	cache.Set("test-key", testData, testContentType, testHeaders, 10*time.Minute)

	entry := cache.Get("test-key")
	if entry == nil {
		t.Fatal("Should be able to get entry after setting")
	}

	if string(entry.Data) != "test data" {
		t.Errorf("Expected data 'test data', got '%s'", string(entry.Data))
	}

	if entry.ContentType != testContentType {
		t.Errorf("Expected content type '%s', got '%s'", testContentType, entry.ContentType)
	}

	if entry.Headers["X-Custom"] != "value" {
		t.Errorf("Expected header X-Custom 'value', got '%s'", entry.Headers["X-Custom"])
	}
}

func TestMemoryCacheExpiration(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 1*time.Millisecond)

	testData := []byte("expiring data")
	cache.Set("expiring-key", testData, "text/plain", nil, 1*time.Millisecond)

	time.Sleep(10 * time.Millisecond)

	entry := cache.Get("expiring-key")
	if entry != nil {
		t.Error("Should not be able to get expired entry")
	}
}

func TestMemoryCacheDelete(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 10*time.Minute)

	cache.Set("delete-key", []byte("data"), "text/plain", nil, 10*time.Minute)

	if !cache.Delete("delete-key") {
		t.Error("Should return true when deleting existing key")
	}

	entry := cache.Get("delete-key")
	if entry != nil {
		t.Error("Should not be able to get deleted entry")
	}

	if cache.Delete("delete-key") {
		t.Error("Should return false when deleting non-existing key")
	}
}

func TestMemoryCacheClear(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 10*time.Minute)

	cache.Set("key1", []byte("data1"), "text/plain", nil, 10*time.Minute)
	cache.Set("key2", []byte("data2"), "text/plain", nil, 10*time.Minute)
	cache.Set("key3", []byte("data3"), "text/plain", nil, 10*time.Minute)

	cache.Clear()

	entry := cache.Get("key1")
	if entry != nil {
		t.Error("Should not be able to get any entry after clear")
	}

	entries, _ := cache.GetStats()
	if entries != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", entries)
	}
}

func TestMemoryCacheLRUEviction(t *testing.T) {
	cache := NewMemoryCache(3, 1024*1024, 10*time.Minute)

	cache.Set("key1", []byte("data1"), "text/plain", nil, 10*time.Minute)
	cache.Set("key2", []byte("data2"), "text/plain", nil, 10*time.Minute)
	cache.Set("key3", []byte("data3"), "text/plain", nil, 10*time.Minute)

	cache.Get("key1")

	cache.Set("key4", []byte("data4"), "text/plain", nil, 10*time.Minute)

	entry := cache.Get("key1")
	if entry == nil {
		t.Error("key1 should still exist (was accessed recently)")
	}

	entry = cache.Get("key2")
	if entry != nil {
		t.Error("key2 should be evicted (least recently used)")
	}
}

func TestMemoryCacheSizeLimit(t *testing.T) {
	cache := NewMemoryCache(100, 100, 10*time.Minute)

	cache.Set("small", []byte("123"), "text/plain", nil, 10*time.Minute)

	cache.Set("large", []byte("1234567890"), "text/plain", nil, 10*time.Minute)

	entries, size := cache.GetStats()
	if size > cache.maxSize {
		t.Errorf("Cache size %d should not exceed max size %d", size, cache.maxSize)
	}
	_ = entries
}

func TestMemoryCacheGetStats(t *testing.T) {
	cache := NewMemoryCache(100, 1024*1024, 10*time.Minute)

	entries, size := cache.GetStats()
	if entries != 0 {
		t.Errorf("Expected 0 entries initially, got %d", entries)
	}

	if size != 0 {
		t.Errorf("Expected 0 size initially, got %d", size)
	}

	cache.Set("key1", []byte("data1"), "text/plain", nil, 10*time.Minute)
	cache.Set("key2", []byte("data2"), "text/plain", nil, 10*time.Minute)

	entries, size = cache.GetStats()
	if entries != 2 {
		t.Errorf("Expected 2 entries, got %d", entries)
	}
}

func TestUniversalCacheSetAndGet(t *testing.T) {
	InitCache()

	testData := []byte("universal cache test")
	GlobalCache.Set("universal-key", testData, "application/json", nil, 10*time.Minute)

	entry := GlobalCache.Get("universal-key")
	if entry == nil {
		t.Fatal("Should be able to get entry from universal cache")
	}

	if string(entry.Data) != "universal cache test" {
		t.Errorf("Expected data 'universal cache test', got '%s'", string(entry.Data))
	}
}

func TestUniversalCacheDelete(t *testing.T) {
	InitCache()

	GlobalCache.Set("delete-universal", []byte("data"), "text/plain", nil, 10*time.Minute)

	if !GlobalCache.Delete("delete-universal") {
		t.Error("Should return true when deleting existing key")
	}

	entry := GlobalCache.Get("delete-universal")
	if entry != nil {
		t.Error("Should not be able to get deleted entry")
	}
}

func TestUniversalCacheClear(t *testing.T) {
	InitCache()

	GlobalCache.Set("clear1", []byte("data1"), "text/plain", nil, 10*time.Minute)
	GlobalCache.Set("clear2", []byte("data2"), "text/plain", nil, 10*time.Minute)

	GlobalCache.Clear()

	entry := GlobalCache.Get("clear1")
	if entry != nil {
		t.Error("Should not be able to get entry after clear")
	}
}

func TestUniversalCacheStats(t *testing.T) {
	InitCache()

	stats := GlobalCache.GetStats()

	if stats.TotalEntries < 0 {
		t.Errorf("Expected non-negative total entries, got %d", stats.TotalEntries)
	}

	if stats.HitRate < 0 || stats.HitRate > 100 {
		t.Errorf("Expected hit rate between 0 and 100, got %f", stats.HitRate)
	}
}

func TestCacheKeyBuilding(t *testing.T) {
	key1 := BuildCacheKey("test", "query")
	key2 := BuildCacheKey("test", "query")
	key3 := BuildCacheKey("test", "different")

	if key1 != key2 {
		t.Error("Same input should produce same key")
	}

	if key1 == key3 {
		t.Error("Different input should produce different key")
	}

	if len(key1) < 10 {
		t.Error("Cache key should have reasonable length")
	}
}

func TestTokenCacheKey(t *testing.T) {
	key := BuildTokenCacheKey("test-token")

	if len(key) < 10 {
		t.Error("Token cache key should have reasonable length")
	}

	key2 := BuildTokenCacheKey("test-token")
	if key != key2 {
		t.Error("Same token should produce same key")
	}
}

func TestManifestCacheKey(t *testing.T) {
	key := BuildManifestCacheKey("image:tag", "reference")

	if len(key) < 10 {
		t.Error("Manifest cache key should have reasonable length")
	}
}

func TestGetManifestTTL(t *testing.T) {
	tests := []struct {
		reference string
		expected  time.Duration
	}{
		{"sha256:abc123", 24 * time.Hour},
		{"latest", 10 * time.Minute},
		{"main", 10 * time.Minute},
		{"master", 10 * time.Minute},
		{"dev", 10 * time.Minute},
		{"develop", 10 * time.Minute},
		{"v1.0.0", 30 * time.Minute},
	}

	for _, tt := range tests {
		ttl := GetManifestTTL(tt.reference)
		if tt.reference == "sha256:abc123" && ttl != 24*time.Hour {
			t.Errorf("Expected TTL for %s to be 24h, got %v", tt.reference, ttl)
		}
	}
}

func BenchmarkMemoryCacheSet(b *testing.B) {
	cache := NewMemoryCache(10000, 500*1024*1024, 10*time.Minute)
	data := []byte("benchmark data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(benchKey(i), data, "text/plain", nil, 10*time.Minute)
	}
}

func BenchmarkMemoryCacheGet(b *testing.B) {
	cache := NewMemoryCache(10000, 500*1024*1024, 10*time.Minute)
	data := []byte("benchmark data")

	for i := 0; i < 10000; i++ {
		cache.Set(benchKey(i), data, "text/plain", nil, 10*time.Minute)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(benchKey(i % 10000))
	}
}

func BenchmarkCacheKeyBuilding(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildCacheKey("test", "query")
	}
}

func benchKey(i int) string {
	return string([]byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)})
}
