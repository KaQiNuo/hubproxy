package utils

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestHTTPClientInitialization(t *testing.T) {
	InitHTTPClients()

	if globalHTTPClient == nil {
		t.Error("Global HTTP client should not be nil")
	}

	if searchHTTPClient == nil {
		t.Error("Search HTTP client should not be nil")
	}

	if proxyHTTPClient == nil {
		t.Error("Proxy HTTP client should not be nil")
	}
}

func TestGlobalHTTPClientConfiguration(t *testing.T) {
	InitHTTPClients()

	client := GetGlobalHTTPClient()
	if client == nil {
		t.Fatal("Global HTTP client is nil")
	}

	transport := client.Transport.(*http.Transport)
	if transport.MaxIdleConns != 500 {
		t.Errorf("Expected MaxIdleConns to be 500, got %d", transport.MaxIdleConns)
	}

	if transport.MaxConnsPerHost != int(globalMaxConnsPerHost) {
		t.Errorf("Expected MaxConnsPerHost to be %d, got %d", globalMaxConnsPerHost, transport.MaxConnsPerHost)
	}
}

func TestSearchHTTPClientConfiguration(t *testing.T) {
	InitHTTPClients()

	client := GetSearchHTTPClient()
	if client == nil {
		t.Fatal("Search HTTP client is nil")
	}

	transport := client.Transport.(*http.Transport)
	if transport.MaxIdleConns != 100 {
		t.Errorf("Expected MaxIdleConns to be 100, got %d", transport.MaxIdleConns)
	}

	if transport.MaxConnsPerHost != int(searchMaxConnsPerHost) {
		t.Errorf("Expected MaxConnsPerHost to be %d, got %d", searchMaxConnsPerHost, transport.MaxConnsPerHost)
	}

	if client.Timeout != 30*time.Second {
		t.Errorf("Expected timeout to be 30s, got %v", client.Timeout)
	}
}

func TestProxyHTTPClientConfiguration(t *testing.T) {
	InitHTTPClients()

	client := GetProxyHTTPClient()
	if client == nil {
		t.Fatal("Proxy HTTP client is nil")
	}

	if client.Timeout != 0 {
		t.Errorf("Expected timeout to be 0 (no timeout), got %v", client.Timeout)
	}

	transport := client.Transport.(*http.Transport)
	if transport.MaxIdleConns != 200 {
		t.Errorf("Expected MaxIdleConns to be 200, got %d", transport.MaxIdleConns)
	}

	if transport.WriteBufferSize != 4*1024*1024 {
		t.Errorf("Expected WriteBufferSize to be 4MB, got %d", transport.WriteBufferSize)
	}

	if transport.ReadBufferSize != 4*1024*1024 {
		t.Errorf("Expected ReadBufferSize to be 4MB, got %d", transport.ReadBufferSize)
	}
}

func TestHTTPClientStats(t *testing.T) {
	InitHTTPClients()

	stats := GetHTTPClientStats()

	if stats["global"] == nil {
		t.Error("Global client stats should not be nil")
	}

	if stats["search"] == nil {
		t.Error("Search client stats should not be nil")
	}

	if stats["proxy"] == nil {
		t.Error("Proxy client stats should not be nil")
	}

	if stats["global"]["maxHost"] != globalMaxConnsPerHost {
		t.Errorf("Expected global maxHost to be %d, got %d", globalMaxConnsPerHost, stats["global"]["maxHost"])
	}
}

func TestSetMaxConnsPerHost(t *testing.T) {
	InitHTTPClients()

	newMaxGlobal := int64(300)
	newMaxSearch := int64(50)
	newMaxProxy := int64(100)

	SetGlobalMaxConnsPerHost(newMaxGlobal)
	SetSearchMaxConnsPerHost(newMaxSearch)
	SetProxyMaxConnsPerHost(newMaxProxy)

	if globalMaxConnsPerHost != newMaxGlobal {
		t.Errorf("Expected globalMaxConnsPerHost to be %d, got %d", newMaxGlobal, globalMaxConnsPerHost)
	}

	if searchMaxConnsPerHost != newMaxSearch {
		t.Errorf("Expected searchMaxConnsPerHost to be %d, got %d", newMaxSearch, searchMaxConnsPerHost)
	}

	if proxyMaxConnsPerHost != newMaxProxy {
		t.Errorf("Expected proxyMaxConnsPerHost to be %d, got %d", newMaxProxy, proxyMaxConnsPerHost)
	}
}

func BenchmarkGlobalHTTPClient(b *testing.B) {
	InitHTTPClients()
	client := GetGlobalHTTPClient()

	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://httpbin.org/get", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Do(req)
	}
}

func BenchmarkSearchHTTPClient(b *testing.B) {
	InitHTTPClients()
	client := GetSearchHTTPClient()

	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://httpbin.org/get", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Do(req)
	}
}

func BenchmarkHTTPClientStats(b *testing.B) {
	InitHTTPClients()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetHTTPClientStats()
	}
}
