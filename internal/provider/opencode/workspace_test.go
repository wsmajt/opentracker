package opencode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListWorkspaceIDs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
		bad  bool
	}{
		{name: "single workspace", body: `[{"id":"wrk_one"}]`, want: []string{"wrk_one"}},
		{name: "multiple and deduplicated organizations", body: `[{"id":"org_one"},{"id":"wrk_two"},{"id":"org_one"}]`, want: []string{"org_one", "wrk_two"}},
		{name: "empty", body: `[]`, bad: true},
		{name: "malformed", body: `[{`, bad: true},
		{name: "missing id", body: `[{"name":"x"}]`, bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/orgs" || r.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected request: %s %s Accept=%q", r.Method, r.URL, r.Header.Get("Accept"))
				}
				if got := r.Header.Get("Cookie"); got != "__Host-console_session=secret" {
					t.Errorf("Cookie header = %q, want only session cookie", got)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			got, err := listWorkspaceIDs([]*http.Cookie{{Name: "__Host-console_session", Value: "secret", Domain: "opencode.ai"}}, server.URL+"/orgs", server.Client())
			if tt.bad {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("IDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListWorkspaceIDsRejectsMissingOrWrongDomainSession(t *testing.T) {
	for _, cookie := range []*http.Cookie{
		{Name: "auth", Value: "secret", Domain: "opencode.ai"},
		{Name: "__Host-console_session", Value: "secret", Domain: ".opencode.ai"},
		{Name: "__Host-console_session", Value: "", Domain: "opencode.ai"},
	} {
		if _, err := listWorkspaceIDs([]*http.Cookie{cookie}, "http://127.0.0.1", http.DefaultClient); err == nil {
			t.Fatalf("accepted cookie %#v", cookie)
		}
	}
}

func TestListWorkspaceIDsRejectsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	if _, err := listWorkspaceIDs(validSessionCookie(), server.URL, server.Client()); err == nil {
		t.Fatal("expected unauthorized error")
	}
}

func TestListWorkspaceIDsCapsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"wrk_one"}]` + strings.Repeat(" ", maxWorkspaceResponseSize)))
	}))
	defer server.Close()
	if _, err := listWorkspaceIDs(validSessionCookie(), server.URL, server.Client()); err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestListWorkspaceIDsBlocksRedirectWithoutForwardingCookie(t *testing.T) {
	var destinationHit bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationHit = true
		if r.Header.Get("Cookie") != "" {
			t.Errorf("cookie forwarded to redirect destination: %q", r.Header.Get("Cookie"))
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if _, err := listWorkspaceIDs(validSessionCookie(), source.URL, client); err == nil {
		t.Fatal("expected redirect error")
	}
	if destinationHit {
		t.Fatal("redirect destination was requested")
	}
}

func validSessionCookie() []*http.Cookie {
	return []*http.Cookie{{Name: "__Host-console_session", Value: "secret", Domain: "opencode.ai"}}
}

func TestDetectWorkspaceIDFromCookiesRejectsMultiple(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"wrk_one"},{"id":"org_two"}]`))
	}))
	defer server.Close()
	if _, err := listWorkspaceIDs(validSessionCookie(), server.URL, server.Client()); err != nil {
		t.Fatal(err)
	}
	if _, err := detectWorkspaceIDFromEndpoint(validSessionCookie(), server.URL, server.Client()); err == nil {
		t.Fatal("expected ambiguous workspace error")
	}
}

func detectWorkspaceIDFromEndpoint(cookies []*http.Cookie, endpoint string, client *http.Client) (string, error) {
	ids, err := listWorkspaceIDs(cookies, endpoint, client)
	return singleWorkspaceID(ids, err)
}
