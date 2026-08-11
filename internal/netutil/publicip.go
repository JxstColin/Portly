// Package netutil provides small network helpers, currently just public IP
// auto-detection so Portly doesn't require the operator to look up and
// type in their VPS's address by hand.
package netutil

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// endpoints are plain-text "what's my IP" services, tried in order until
// one responds with something that parses as an IP address. Multiple are
// used since any single one can be down, rate-limiting, or blocked.
var endpoints = []string{
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
	"https://ipinfo.io/ip",
	"https://api.ipify.org",
}

// DetectPublicIP asks a handful of external services what our outbound IP
// looks like, returning the first valid answer.
func DetectPublicIP() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	var lastErr error
	for _, url := range endpoints {
		ip, err := fetchIP(client, url)
		if err != nil {
			lastErr = err
			continue
		}
		return ip, nil
	}
	return "", fmt.Errorf("could not detect public IP from any provider: %w", lastErr)
}

func fetchIP(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("%s returned a non-IP response: %q", url, ip)
	}
	return ip, nil
}
