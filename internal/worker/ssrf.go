package worker

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Extra ranges that net.IP's helpers don't cover.
var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8",     // "this network"
		"100.64.0.0/10", // carrier-grade NAT, used inside some cloud networks
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
	} {
		_, n, _ := net.ParseCIDR(cidr)
		out = append(out, n)
	}
	return out
}()

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || // 169.254.x.x = cloud metadata
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// newHTTPClient builds the client used for deliveries.
//
// SSRF protection lives in Dialer.Control, which runs AFTER DNS resolution with
// the actual IP being connected to. Checking the URL when the endpoint is
// registered isn't enough: an attacker can point their domain at a public IP,
// register it, then switch the DNS record to 127.0.0.1 (DNS rebinding).
// Checking at connect time catches that.
func newHTTPClient(allowPrivate bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || isBlockedIP(ip) {
				return fmt.Errorf("destination %s is a private or reserved address (blocked for SSRF safety)", host)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // never route deliveries through an env-configured proxy
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Don't follow redirects: a redirect to http://127.0.0.1 would be another
		// SSRF route, and webhook receivers should answer directly anyway.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
