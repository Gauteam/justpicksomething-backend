package linktitle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestCheckURL(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://www.airbnb.com/rooms/123", true},
		{"http://example.com/page", true},
		{"https://example.com:443/x", true},
		{"ftp://example.com/file", false},
		{"file:///etc/passwd", false},
		{"javascript:alert(1)", false},
		{"https://example.com:8080/", false},
		{"http://localhost/", false},
		{"http://api.localhost/", false},
		{"http://metadata.google.internal/", false},
		{"http://127.0.0.1/", false},
		{"http://10.0.0.5/", false},
		{"http://192.168.1.1/", false},
		{"http://169.254.169.254/latest/meta-data/", false},
		{"http://100.64.0.1/", false},
		{"http://[::1]/", false},
		{"http://[fd00::1]/", false},
		{"http://user:pass@example.com/", false},
		{"http:///nohost", false},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.url)
		if err != nil {
			if tt.ok {
				t.Errorf("%s: parse error %v", tt.url, err)
			}
			continue
		}
		got := CheckURL(u) == nil
		if got != tt.ok {
			t.Errorf("CheckURL(%s) ok = %v, want %v", tt.url, got, tt.ok)
		}
	}
}

func TestIsPublic(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.1.1",
		"100.64.1.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "224.0.0.1", "::ffff:127.0.0.1"} {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s should not be public", s)
		}
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name, page, want string
	}{
		{"title", `<html><head><title>Lakeview  sauna
 retreat &amp; spa</title></head>`, "Lakeview sauna retreat & spa"},
		{"og fallback", `<head><title> </title><meta property="og:title" content="Cabin · ★4.95"></head>`, "Cabin · ★4.95"},
		{"og content first", `<meta content="Hotel Zed" property="og:title">`, "Hotel Zed"},
		{"none", `<html><body>no title</body></html>`, ""},
		{"long", "<title>" + strings.Repeat("a", 300) + "</title>", strings.Repeat("a", 199) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Extract(tt.page); got != tt.want {
				t.Errorf("Extract = %q, want %q", got, tt.want)
			}
		})
	}
}

// A local test server must be unreachable. (httptest uses a random port, which
// the port rule already refuses; the connect-time IP rule is covered by
// TestIsPublic, since it uses the same check.)
func TestTitleRefusesLocalServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<title>secret</title>"))
	}))
	defer srv.Close()

	_, err := New().Title(context.Background(), srv.URL)
	if !errors.Is(err, ErrNotAllowed) {
		t.Errorf("direct loopback IP: err = %v, want ErrNotAllowed", err)
	}
}
