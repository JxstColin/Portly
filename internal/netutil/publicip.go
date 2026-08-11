// Package netutil provides small network helpers, currently just public IP
// auto-detection so Portly doesn't require the operator to look up and
// type in their VPS's address by hand.
package netutil

import (
	"context"
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

// DetectPublicIP asks a handful of external services what our outbound IPv4
// address looks like, returning the first valid answer. It's pinned to
// IPv4 deliberately: on a dual-stack host, Go's default dialer prefers
// IPv6 when a route exists, which previously meant a VPS with working
// IPv6 would "detect" an IPv6 address as its public IP — useless for an A
// record, and silently broken for any client without IPv6 (e.g. many home
// networks/containers). IPv4 remains the safe default nearly everyone can
// reach; see DetectPublicIPv6 for the optional AAAA counterpart.
func DetectPublicIP() (string, error) {
	return detect(context.Background(), "tcp4")
}

// DetectPublicIPv6 is the IPv6 counterpart to DetectPublicIP, for operators
// who additionally want an AAAA record. It's expected to fail (and that's
// fine — callers should treat it as optional) on hosts without IPv6.
func DetectPublicIPv6(ctx context.Context) (string, error) {
	return detect(ctx, "tcp6")
}

func detect(ctx context.Context, network string) (string, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}

	var lastErr error
	for _, url := range endpoints {
		ip, err := fetchIP(ctx, client, url)
		if err != nil {
			lastErr = err
			continue
		}
		return ip, nil
	}
	return "", fmt.Errorf("could not detect public IP from any provider: %w", lastErr)
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
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
