package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeURLs(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		raw     string
		policy  Policy
		wantErr error
	}{
		{"http", "http://example.com/v1", Policy{}, ErrInsecureScheme},
		{"loopback", "https://127.0.0.1/v1", Policy{}, ErrPrivateAddress},
		{"ipv6 loopback", "https://[::1]/v1", Policy{}, ErrPrivateAddress},
		{"metadata", "https://169.254.169.254/v1", Policy{}, ErrPrivateAddress},
		{"path required", "https://example.com", Policy{}, ErrPathRequired},
		{"userinfo", "https://user:pass@example.com/v1", Policy{}, ErrUserInfo},
		{"query", "https://example.com/v1?x=1", Policy{}, ErrQueryOrFragment},
		{"host allowlist", "https://example.com/v1", Policy{AllowedHosts: []string{"api.openai.com"}}, ErrHostNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.policy.Validate(ctx, tt.raw); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate(%q) error = %v, want %v", tt.raw, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAcceptsPublicIP(t *testing.T) {
	got, err := (Policy{}).Validate(context.Background(), "https://1.1.1.1/v1/")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got != "https://1.1.1.1/v1" {
		t.Fatalf("Validate() = %q", got)
	}
}

func TestValidateAllowsPrivateForDev(t *testing.T) {
	got, err := (Policy{AllowHTTP: true, AllowPrivate: true}).Validate(context.Background(), "http://127.0.0.1:8080/v1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got != "http://127.0.0.1:8080/v1" {
		t.Fatalf("Validate() = %q", got)
	}
}

func TestIsDeniedIP(t *testing.T) {
	denied := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fc00::1",
	}
	for _, raw := range denied {
		if !isDeniedIP(net.ParseIP(raw)) {
			t.Fatalf("%s 应当被拒绝", raw)
		}
	}
	if isDeniedIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("1.1.1.1 不应被拒绝")
	}
}

func TestHTTPClientBlocksPrivateAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient(Policy{AllowHTTP: true}, 2*time.Second)
	if _, err := client.Get(server.URL); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("Get() error = %v, want ErrPrivateAddress", err)
	}
}

func TestHTTPClientRejectsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	client := NewHTTPClient(Policy{AllowHTTP: true, AllowPrivate: true}, 2*time.Second)
	if _, err := client.Get(server.URL); !errors.Is(err, ErrRedirect) {
		t.Fatalf("Get() error = %v, want ErrRedirect", err)
	}
}
