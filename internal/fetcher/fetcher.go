package fetcher

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Fetcher struct {
	client  *http.Client
	cookies []*http.Cookie
}

func New(cookieFile string) (*Fetcher, error) {
	cookies, err := loadNetscapeCookies(cookieFile)
	if err != nil {
		return nil, err
	}

	return &Fetcher{
		client:  &http.Client{Timeout: 30 * time.Second},
		cookies: cookies,
	}, nil
}

func (f *Fetcher) Get(ctx context.Context, targetURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	u, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	for _, c := range f.cookies {
		if domainMatches(u.Host, c.Domain) && strings.HasPrefix(u.Path, c.Path) {
			req.AddCookie(c)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

func loadNetscapeCookies(path string) ([]*http.Cookie, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot open cookie file: %w", err)
	}
	defer f.Close()

	var cookies []*http.Cookie
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}

		domain := fields[0]
		// Strip #HttpOnly_ prefix from domain (Netscape format for HttpOnly cookies)
		domain = strings.TrimPrefix(domain, "#HttpOnly_")
		// fields[1] = flag (TRUE/FALSE)
		path := fields[2]
		secure := fields[3] == "TRUE"
		// fields[4] = expiration timestamp
		name := fields[5]
		value := fields[6]

		expFloat, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			continue
		}
		exp := int64(expFloat)

		cookies = append(cookies, &http.Cookie{
			Name:     name,
			Value:    value,
			Domain:   domain,
			Path:     path,
			Secure:   secure,
			Expires:  time.Unix(exp, 0),
			HttpOnly: false,
		})
	}

	return cookies, scanner.Err()
}

// CookieHeader returns a semicolon-separated cookie string for the given host.
func (f *Fetcher) CookieHeader(host string) string {
	host = strings.ToLower(host)
	var parts []string
	for _, c := range f.cookies {
		if domainMatches(host, c.Domain) {
			parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
		}
	}
	return strings.Join(parts, "; ")
}

func domainMatches(host, domain string) bool {
	host = strings.ToLower(host)
	domain = strings.ToLower(domain)
	if strings.HasPrefix(domain, ".") {
		return host == domain[1:] || strings.HasSuffix(host, domain)
	}
	return host == domain
}
