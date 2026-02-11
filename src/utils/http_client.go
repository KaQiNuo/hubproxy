package utils

import (
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"hubproxy/config"
)

var (
	globalHTTPClient           *http.Client
	searchHTTPClient           *http.Client
	proxyHTTPClient           *http.Client
	globalConns               int64
	searchConns               int64
	proxyConns                int64
	globalMaxConnsPerHost     int64 = 200
	searchMaxConnsPerHost     int64 = 20
	proxyMaxConnsPerHost      int64 = 50
)

type connectionTracker struct {
	totalConns    int64
	activeConns   int64
	idleConns     int64
	lastReset     time.Time
}

var (
	globalTracker     = &connectionTracker{}
	searchTracker     = &connectionTracker{}
	proxyTracker      = &connectionTracker{}
)

func (t *connectionTracker) increment(total, active bool) {
	atomic.AddInt64(&t.totalConns, 1)
	if active {
		atomic.AddInt64(&t.activeConns, 1)
	}
}

func (t *connectionTracker) decrement(active bool) {
	atomic.AddInt64(&t.totalConns, -1)
	if active {
		atomic.AddInt64(&t.activeConns, -1)
	}
}

func (t *connectionTracker) setIdle(count int64) {
	atomic.StoreInt64(&t.idleConns, count)
}

func (t *connectionTracker) getStats() (total, active, idle int64) {
	return atomic.LoadInt64(&t.totalConns),
		atomic.LoadInt64(&t.activeConns),
		atomic.LoadInt64(&t.idleConns)
}

func (t *connectionTracker) reset() {
	now := time.Now()
	if now.Sub(t.lastReset) > time.Minute {
		atomic.StoreInt64(&t.totalConns, 0)
		atomic.StoreInt64(&t.activeConns, 0)
		atomic.StoreInt64(&t.idleConns, 0)
		t.lastReset = now
	}
}

func InitHTTPClients() {
	cfg := config.GetConfig()

	if p := cfg.Access.Proxy; p != "" {
		os.Setenv("HTTP_PROXY", p)
		os.Setenv("HTTPS_PROXY", p)
	}

	globalHTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          500,
			MaxIdleConnsPerHost:   100,
			MaxConnsPerHost:       int(globalMaxConnsPerHost),
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 300 * time.Second,
			DisableCompression:    false,
			DisableKeepAlives:     false,
		},
	}

	searchHTTPClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			MaxConnsPerHost:       int(searchMaxConnsPerHost),
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			DisableCompression:    false,
			DisableKeepAlives:     false,
		},
	}

	proxyHTTPClient = &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   50,
			MaxConnsPerHost:       int(proxyMaxConnsPerHost),
			IdleConnTimeout:       120 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			DisableCompression:    false,
			DisableKeepAlives:     false,
			WriteBufferSize:       4 * 1024 * 1024,
			ReadBufferSize:        4 * 1024 * 1024,
		},
	}
}

func GetGlobalHTTPClient() *http.Client {
	globalTracker.reset()
	return globalHTTPClient
}

func GetSearchHTTPClient() *http.Client {
	searchTracker.reset()
	return searchHTTPClient
}

func GetProxyHTTPClient() *http.Client {
	proxyTracker.reset()
	return proxyHTTPClient
}

func GetHTTPClientStats() map[string]map[string]int64 {
	return map[string]map[string]int64{
		"global": {
			"total":   globalTracker.totalConns,
			"active":  globalTracker.activeConns,
			"idle":    globalTracker.idleConns,
			"maxHost": globalMaxConnsPerHost,
		},
		"search": {
			"total":   searchTracker.totalConns,
			"active":  searchTracker.activeConns,
			"idle":    searchTracker.idleConns,
			"maxHost": searchMaxConnsPerHost,
		},
		"proxy": {
			"total":   proxyTracker.totalConns,
			"active":  proxyTracker.activeConns,
			"idle":    proxyTracker.idleConns,
			"maxHost": proxyMaxConnsPerHost,
		},
	}
}

func SetGlobalMaxConnsPerHost(max int64) {
	atomic.StoreInt64(&globalMaxConnsPerHost, max)
}

func SetSearchMaxConnsPerHost(max int64) {
	atomic.StoreInt64(&searchMaxConnsPerHost, max)
}

func SetProxyMaxConnsPerHost(max int64) {
	atomic.StoreInt64(&proxyMaxConnsPerHost, max)
}
