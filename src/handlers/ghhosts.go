package handlers

import (
	"context"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

var githubDomains = []string{
	"github.com",
	"api.github.com",
	"assets-cdn.github.com",
	"raw.githubusercontent.com",
	"gist.github.com",
	"user-images.githubusercontent.com",
	"avatars.githubusercontent.com",
	"favicons.githubusercontent.com",
	"camo.githubusercontent.com",
	"github.githubassets.com",
	"collector.github.com",
	"objects.githubusercontent.com",
	"pipelines.actions.githubusercontent.com",
	"copilot.githubusercontent.com",
	"central.github.com",
	"desktop.github.com",
}

type ghDomainResult struct {
	Domain    string   `json:"domain"`
	IPs       []string `json:"ips"`
	LatencyMs int64    `json:"latency_ms"`
	Error     string   `json:"error,omitempty"`
}

func CreateGHHostsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		r := &net.Resolver{PreferGo: true}
		results := make([]ghDomainResult, 0, len(githubDomains))

		type job struct {
			domain string
			result ghDomainResult
		}
		ch := make(chan job, len(githubDomains))

		for _, d := range githubDomains {
			d := d
			go func() {
				start := time.Now()
				ips, err := r.LookupHost(context.Background(), d)
				latencyMs := time.Since(start).Milliseconds()
				res := ghDomainResult{Domain: d, LatencyMs: latencyMs}
				if err != nil {
					res.Error = err.Error()
				} else {
					var v4 []string
					for _, ip := range ips {
						if net.ParseIP(ip) != nil && net.ParseIP(ip).To4() != nil {
							v4 = append(v4, ip)
						}
					}
					sort.Slice(v4, func(i, j int) bool { return v4[i] < v4[j] })
					res.IPs = v4
				}
				ch <- job{d, res}
			}()
		}

		for i := 0; i < len(githubDomains); i++ {
			j := <-ch
			results = append(results, j.result)
		}

		sort.Slice(results, func(i, j int) bool { return results[i].Domain < results[j].Domain })
		c.JSON(http.StatusOK, gin.H{"domains": results, "count": len(results), "updated_at": time.Now().Unix()})
	}
}
