package utils

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	sync_atomic "sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"hubproxy/config"
)

type CacheTier string

const (
	L1Memory CacheTier = "memory"
	L2Disk   CacheTier = "disk"
)

type CacheEntry struct {
	Data        []byte
	ContentType string
	Headers     map[string]string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	AccessCount int64
	LastAccess  time.Time
	CacheTier   CacheTier
	Key         string
	Size        int64
}

type CacheStats struct {
	TotalEntries    int64
	MemoryEntries   int64
	DiskEntries     int64
	TotalSize       int64
	Hits            int64
	Misses          int64
	HitRate         float64
	Evictions       int64
	ExpiredCleanups int64
}

type CacheConfig struct {
	MaxMemoryEntries  int
	MaxDiskEntries    int
	MaxMemorySize     int64
	MaxDiskSize       int64
	MemoryTTL         time.Duration
	DiskTTL           time.Duration
	CacheDir          string
	EnableDiskCache   bool
	EnableCompression bool
	EvictionPolicy    string
}

type UniversalCache struct {
	l1Cache *MemoryCache
	l2Cache *DiskCache
	config  *CacheConfig
	mu      sync.RWMutex
	stats   CacheStats
}

type LinkedList struct {
	head *ListNode
	tail *ListNode
	size int
}

type ListNode struct {
	key  string
	next *ListNode
	prev *ListNode
}

type MemoryCache struct {
	data        map[string]*CacheEntry
	maxEntries  int
	maxSize     int64
	currentSize int64
	defaultTTL  time.Duration
	accessOrder *LinkedList
	mu          sync.RWMutex
}

type DiskCache struct {
	cacheDir string
	maxSize  int64
	ttl      time.Duration
	index    *MemoryCache
	mu       sync.RWMutex
}

var (
	GlobalCache *UniversalCache
	cacheOnce   sync.Once
)

func NewLinkedList() *LinkedList {
	return &LinkedList{
		head: &ListNode{},
		tail: &ListNode{},
	}
}

func (l *LinkedList) MoveToFront(node *ListNode) {
	if node.prev == l.head {
		return
	}

	node.prev.next = node.next
	if node.next != nil {
		node.next.prev = node.prev
	} else {
		l.tail = node.prev
	}

	node.prev = l.head
	node.next = l.head.next
	l.head.next = node
	if l.tail == node {
		l.tail = node.prev
	}
}

func (l *LinkedList) PushFront(key string) *ListNode {
	node := &ListNode{
		key:  key,
		next: l.head.next,
	}

	if l.head.next != nil {
		l.head.next.prev = node
	} else {
		l.tail = node
	}

	l.head.next = node
	node.prev = l.head
	l.size++

	return node
}

func (l *LinkedList) Remove(node *ListNode) {
	if node.prev != nil {
		node.prev.next = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	}
	if l.tail == node {
		l.tail = node.prev
	}
	l.size--
}

func NewMemoryCache(maxEntries int, maxSize int64, defaultTTL time.Duration) *MemoryCache {
	return &MemoryCache{
		data:        make(map[string]*CacheEntry),
		maxEntries:  maxEntries,
		maxSize:     maxSize,
		defaultTTL:  defaultTTL,
		accessOrder: NewLinkedList(),
	}
}

func (c *MemoryCache) Get(key string) *CacheEntry {
	c.mu.RLock()
	entry, exists := c.data[key]
	c.mu.RUnlock()

	if !exists {
		return nil
	}

	if time.Now().After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.data, key)
		c.currentSize -= entry.Size
		if node := c.accessOrder.tail; node != nil && node != c.accessOrder.head {
			c.accessOrder.Remove(node)
		}
		c.mu.Unlock()
		return nil
	}

	c.mu.Lock()
	entry.AccessCount++
	entry.LastAccess = time.Now()
	if node := c.accessOrder.head.next; node != nil {
		c.accessOrder.MoveToFront(node)
	}
	c.mu.Unlock()

	return entry
}

func (c *MemoryCache) Set(key string, data []byte, contentType string, headers map[string]string, ttl time.Duration) {
	if ttl == 0 {
		ttl = c.defaultTTL
	}

	entry := &CacheEntry{
		Data:        data,
		ContentType: contentType,
		Headers:     headers,
		ExpiresAt:   time.Now().Add(ttl),
		CreatedAt:   time.Now(),
		AccessCount: 0,
		LastAccess:  time.Now(),
		CacheTier:   L1Memory,
		Key:         key,
		Size:        int64(len(data)),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, exists := c.data[key]; exists {
		c.currentSize -= existing.Size
		delete(c.data, key)
	}

	for c.currentSize+entry.Size > c.maxSize || len(c.data) >= c.maxEntries {
		if c.accessOrder.tail == nil || c.accessOrder.tail == c.accessOrder.head {
			break
		}

		lruNode := c.accessOrder.tail.prev
		if lruNode == nil || lruNode == c.accessOrder.head {
			break
		}

		if existing, exists := c.data[lruNode.key]; exists {
			c.currentSize -= existing.Size
			delete(c.data, lruNode.key)
		}
		c.accessOrder.Remove(lruNode)
	}

	c.accessOrder.PushFront(key)
	c.data[key] = entry
	c.currentSize += entry.Size
}

func (c *MemoryCache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.data[key]; exists {
		delete(c.data, key)
		c.currentSize -= entry.Size
		return true
	}
	return false
}

func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = make(map[string]*CacheEntry)
	c.currentSize = 0
	c.accessOrder = NewLinkedList()
}

func (c *MemoryCache) GetStats() (entries int, size int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data), c.currentSize
}

func NewDiskCache(cacheDir string, maxSize int64, ttl time.Duration) *DiskCache {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil
	}

	dc := &DiskCache{
		cacheDir: cacheDir,
		maxSize:  maxSize,
		ttl:      ttl,
		index:    NewMemoryCache(50000, 50*1024*1024, ttl),
	}

	go dc.startCleanupWorker()
	return dc
}

func (dc *DiskCache) getCachePath(key string) string {
	hash := fmt.Sprintf("%x", md5.Sum([]byte(key)))
	return filepath.Join(dc.cacheDir, hash[:2], hash[2:4], hash)
}

func (dc *DiskCache) Get(key string) ([]byte, error) {
	dc.mu.RLock()
	entry := dc.index.Get(key)
	dc.mu.RUnlock()

	if entry == nil {
		return nil, nil
	}

	path := dc.getCachePath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		dc.Delete(key)
		return nil, nil
	}

	if dc.index.Delete(key) {
		os.Remove(path)
	}

	return data, nil
}

func (dc *DiskCache) Set(key string, data []byte) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	path := dc.getCachePath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	dc.index.Set(key, data, "application/octet-stream", nil, dc.ttl)
	return nil
}

func (dc *DiskCache) Delete(key string) bool {
	path := dc.getCachePath(key)

	dc.mu.Lock()
	defer dc.mu.Unlock()

	if dc.index.Delete(key) {
		os.Remove(path)
		return true
	}
	return false
}

func (dc *DiskCache) Clear() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.index.Clear()
	os.RemoveAll(dc.cacheDir)
	os.MkdirAll(dc.cacheDir, 0755)
}

func (dc *DiskCache) startCleanupWorker() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		dc.mu.Lock()
		dc.cleanupExpired()
		dc.mu.Unlock()
	}
}

func (dc *DiskCache) cleanupExpired() {
	now := time.Now()
	dc.index.mu.Lock()
	defer dc.index.mu.Unlock()

	for key, entry := range dc.index.data {
		if now.After(entry.ExpiresAt) {
			delete(dc.index.data, key)
			path := dc.getCachePath(key)
			go os.Remove(path)
		}
	}
}

func InitCache() {
	cacheOnce.Do(func() {
		cfg := config.GetConfig()

		cacheConfig := &CacheConfig{
			MaxMemoryEntries:  10000,
			MaxDiskEntries:    50000,
			MaxMemorySize:     500 * 1024 * 1024,
			MaxDiskSize:       2 * 1024 * 1024 * 1024,
			MemoryTTL:         30 * time.Minute,
			DiskTTL:           24 * time.Hour,
			CacheDir:          "cache",
			EnableDiskCache:   true,
			EnableCompression: false,
			EvictionPolicy:    "lru",
		}

		if cfg.TokenCache.Enabled {
			cacheConfig.MemoryTTL, _ = time.ParseDuration(cfg.TokenCache.DefaultTTL)
		}

		GlobalCache = &UniversalCache{
			l1Cache: NewMemoryCache(cacheConfig.MaxMemoryEntries, cacheConfig.MaxMemorySize, cacheConfig.MemoryTTL),
			config:  cacheConfig,
		}

		if cacheConfig.EnableDiskCache {
			GlobalCache.l2Cache = NewDiskCache(
				cacheConfig.CacheDir,
				cacheConfig.MaxDiskSize,
				cacheConfig.DiskTTL,
			)
		}

		go GlobalCache.startCleanupWorker()
	})
}

func (c *UniversalCache) Get(key string) *CacheEntry {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	entry := c.l1Cache.Get(key)
	c.mu.RUnlock()

	if entry != nil {
		atomicAddInt64(&c.stats.Hits, 1)
		return entry
	}

	atomicAddInt64(&c.stats.Misses, 1)

	if c.l2Cache != nil {
		data, err := c.l2Cache.Get(key)
		if err == nil && data != nil {
			c.mu.Lock()
			c.l1Cache.Set(key, data, "application/octet-stream", nil, 0)
			c.mu.Unlock()

			entry = c.l1Cache.Get(key)
			return entry
		}
	}

	return nil
}

func (c *UniversalCache) Set(key string, data []byte, contentType string, headers map[string]string, ttl time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.l1Cache.Set(key, data, contentType, headers, ttl)

	if c.l2Cache != nil {
		c.l2Cache.Set(key, data)
	}
}

func (c *UniversalCache) Delete(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	deleted := c.l1Cache.Delete(key)
	if c.l2Cache != nil {
		if c.l2Cache.Delete(key) {
			deleted = true
		}
	}
	return deleted
}

func (c *UniversalCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.l1Cache.Clear()
	if c.l2Cache != nil {
		c.l2Cache.Clear()
	}
}

func (c *UniversalCache) GetStats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	memoryEntries, memorySize := c.l1Cache.GetStats()
	diskEntries, _ := 0, int64(0)
	if c.l2Cache != nil {
		diskEntries, _ = c.l2Cache.index.GetStats()
	}

	totalHits := atomicLoadInt64(&c.stats.Hits)
	totalMisses := atomicLoadInt64(&c.stats.Misses)
	total := totalHits + totalMisses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(totalHits) / float64(total) * 100
	}

	return CacheStats{
		TotalEntries:    int64(memoryEntries + diskEntries),
		MemoryEntries:   int64(memoryEntries),
		DiskEntries:     int64(diskEntries),
		TotalSize:       memorySize,
		Hits:            totalHits,
		Misses:          totalMisses,
		HitRate:         hitRate,
		Evictions:       atomicLoadInt64(&c.stats.Evictions),
		ExpiredCleanups: atomicLoadInt64(&c.stats.ExpiredCleanups),
	}
}

func (c *UniversalCache) startCleanupWorker() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		c.l1Cache.mu.Lock()
		c.cleanupExpiredEntries()
		c.l1Cache.mu.Unlock()
		c.mu.Unlock()
	}
}

func (c *UniversalCache) cleanupExpiredEntries() {
	now := time.Now()
	deleted := 0

	for key, entry := range c.l1Cache.data {
		if now.After(entry.ExpiresAt) {
			delete(c.l1Cache.data, key)
			c.l1Cache.currentSize -= entry.Size
			deleted++

			if c.l2Cache != nil {
				c.l2Cache.Delete(key)
			}
		}
	}

	if deleted > 0 {
		atomicAddInt64(&c.stats.ExpiredCleanups, int64(deleted))
	}
}

func atomicAddInt64(addr *int64, delta int64) {
	sync_atomic.AddInt64(addr, delta)
}

func atomicLoadInt64(addr *int64) int64 {
	return sync_atomic.LoadInt64(addr)
}

type CachedItem struct {
	Data        []byte
	ContentType string
	Headers     map[string]string
	ExpiresAt   time.Time
}

func (c *UniversalCache) GetCachedItem(key string) *CachedItem {
	if c == nil {
		return nil
	}
	if entry := c.Get(key); entry != nil {
		return &CachedItem{
			Data:        entry.Data,
			ContentType: entry.ContentType,
			Headers:     entry.Headers,
			ExpiresAt:   entry.ExpiresAt,
		}
	}
	return nil
}

func (c *UniversalCache) GetToken(key string) string {
	if entry := c.Get(key); entry != nil {
		return string(entry.Data)
	}
	return ""
}

func (c *UniversalCache) SetToken(key string, value string, ttl time.Duration) {
	c.Set(key, []byte(value), "application/json", nil, ttl)
}

func BuildCacheKey(prefix, query string) string {
	return fmt.Sprintf("%s:%x", prefix, md5.Sum([]byte(query)))
}

func BuildTokenCacheKey(query string) string {
	return BuildCacheKey("token", query)
}

func BuildManifestCacheKey(imageRef, reference string) string {
	key := fmt.Sprintf("%s:%s", imageRef, reference)
	return BuildCacheKey("manifest", key)
}

func GetManifestTTL(reference string) time.Duration {
	cfg := config.GetConfig()
	defaultTTL := 30 * time.Minute
	if cfg.TokenCache.DefaultTTL != "" {
		if parsed, err := time.ParseDuration(cfg.TokenCache.DefaultTTL); err == nil {
			defaultTTL = parsed
		}
	}

	if strings.HasPrefix(reference, "sha256:") {
		return 24 * time.Hour
	}

	if reference == "latest" || reference == "main" || reference == "master" ||
		reference == "dev" || reference == "develop" {
		return 10 * time.Minute
	}

	return defaultTTL
}

func ExtractTTLFromResponse(responseBody []byte) time.Duration {
	var tokenResp struct {
		ExpiresIn int `json:"expires_in"`
	}

	defaultTTL := 30 * time.Minute

	if json.Unmarshal(responseBody, &tokenResp) == nil && tokenResp.ExpiresIn > 0 {
		safeTTL := time.Duration(tokenResp.ExpiresIn-300) * time.Second
		if safeTTL > 5*time.Minute {
			return safeTTL
		}
	}

	return defaultTTL
}

func WriteTokenResponse(c *gin.Context, cachedBody string) {
	c.Header("Content-Type", "application/json")
	c.String(200, cachedBody)
}

func WriteCachedResponse(c *gin.Context, item *CachedItem) {
	if item.ContentType != "" {
		c.Header("Content-Type", item.ContentType)
	}

	for key, value := range item.Headers {
		c.Header(key, value)
	}

	c.Data(200, item.ContentType, item.Data)
}

func IsCacheEnabled() bool {
	cfg := config.GetConfig()
	return cfg.TokenCache.Enabled
}

func IsTokenCacheEnabled() bool {
	return IsCacheEnabled()
}
