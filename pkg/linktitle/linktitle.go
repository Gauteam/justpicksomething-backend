// Package linktitle fetches a web page's title (what a browser shows on the
// tab) for a URL someone pasted. The URL comes from a user, so every fetch is
// locked down against server-side request forgery: public http(s) hosts only,
// checked at connect time (after DNS) and on every redirect.
package linktitle

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	timeout      = 5 * time.Second
	maxBodyBytes = 1 << 20 // titles are in <head>; 1 MB is plenty
	maxRedirects = 3
	maxTitleLen  = 200
	userAgent    = "JustPickSomething/1.0 (+https://pick.rajwalgautam.com)"
)

var ErrNotAllowed = errors.New("url not allowed")

// Fetcher fetches page titles. The zero value is not usable; use New.
type Fetcher struct {
	client *http.Client
}

// New returns a Fetcher whose connections can only reach public addresses.
func New() *Fetcher {
	dialer := &net.Dialer{
		Timeout: timeout,
		// Runs after DNS resolution, on the actual IP being connected to, so
		// a hostname that resolves to a private address is refused too.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if port != "80" && port != "443" {
				return ErrNotAllowed
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !IsPublic(ip) {
				return ErrNotAllowed
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // never route through an environment proxy
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          2,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return CheckURL(req.URL)
		},
	}
	return &Fetcher{client: client}
}

// CheckURL rejects anything but http(s) on the default ports with a host name
// or public IP. (Host names are checked again after DNS, in the dialer.)
func CheckURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrNotAllowed
	}
	if u.User != nil || u.Hostname() == "" {
		return ErrNotAllowed
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return ErrNotAllowed
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !IsPublic(ip) {
		return ErrNotAllowed
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
		return ErrNotAllowed
	}
	return nil
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// IsPublic reports whether ip is a globally routable unicast address.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() &&
		ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified() &&
		!cgnat.Contains(ip)
}

// Title fetches rawURL and returns its page title, or "" if there isn't one.
func (f *Fetcher) Title(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", ErrNotAllowed
	}
	if err := CheckURL(u); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	res, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return Extract(string(body)), nil
}

var (
	titleRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	ogTitleRe = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]*content=["']([^"']*)["']|<meta[^>]+content=["']([^"']*)["'][^>]*property=["']og:title["']`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// Extract pulls the page title out of HTML: <title>, else og:title. The
// result is unescaped, whitespace-collapsed and capped at 200 characters.
func Extract(page string) string {
	var t string
	if m := titleRe.FindStringSubmatch(page); m != nil {
		t = m[1]
	}
	if strings.TrimSpace(t) == "" {
		if m := ogTitleRe.FindStringSubmatch(page); m != nil {
			t = m[1] + m[2]
		}
	}
	t = strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(t), " "))
	if utf8.RuneCountInString(t) > maxTitleLen {
		t = string([]rune(t)[:maxTitleLen-1]) + "…"
	}
	return t
}
