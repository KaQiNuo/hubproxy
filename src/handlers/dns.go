package handlers

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type dnsLookupRequest struct {
	Server string `json:"server"`
	Host   string `json:"host"`
}

func CreateDNSLookupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dnsLookupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.Server == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "server required"})
			return
		}
		if req.Host == "" {
			req.Host = "www.google.com"
		}

		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", net.JoinHostPort(req.Server, "53"))
			},
		}

		start := time.Now()
		ips4, err4 := r.LookupHost(context.Background(), req.Host)
		latencyMs := time.Since(start).Milliseconds()

		ips6 := []string{}
		if err4 == nil {
			for _, ip := range ips4 {
				if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() == nil {
					ips6 = append(ips6, ip)
				}
			}
			ips4 = filterIPv4(ips4)
		}

		if err4 != nil {
			c.JSON(http.StatusOK, gin.H{
				"host":       req.Host,
				"server":     req.Server,
				"latency_ms": latencyMs,
				"error":      err4.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"host":       req.Host,
			"server":     req.Server,
			"latency_ms": latencyMs,
			"ips_v4":     ips4,
			"ips_v6":     ips6,
		})
	}
}

func filterIPv4(ips []string) []string {
	var v4 []string
	for _, ip := range ips {
		if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() != nil {
			v4 = append(v4, ip)
		}
	}
	return v4
}
