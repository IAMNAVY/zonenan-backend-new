package ids

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestVerifyCASTicket(t *testing.T) {
	const service = "http://ca.csu.edu.cn/personalInfo/personCenter/index.html"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/serviceValidate" {
			t.Errorf("path = %q, want /serviceValidate", r.URL.Path)
		}
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("service = %q, want %q", got, service)
		}
		if got := r.URL.Query().Get("ticket"); got != "ST-test" {
			t.Errorf("ticket = %q, want ST-test", got)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas"><cas:authenticationSuccess><cas:user>2020123456</cas:user></cas:authenticationSuccess></cas:serviceResponse>`)
	}))
	defer server.Close()

	verifier := NewVerifierWithCASService(server.URL, "", "", service)
	got, err := verifier.VerifyCASTicket(t.Context(), "ST-test")
	if err != nil {
		t.Fatalf("VerifyCASTicket() error = %v", err)
	}
	if got != "2020123456" {
		t.Fatalf("VerifyCASTicket() = %q, want 2020123456", got)
	}
}

func TestVerifyCASTicketRejectsInvalidResponses(t *testing.T) {
	const service = "https://example.test/cas-service"
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "invalid ticket",
			status: http.StatusOK,
			body:   `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas"><cas:authenticationFailure code="INVALID_TICKET"/></cas:serviceResponse>`,
		},
		{
			name:   "missing user",
			status: http.StatusOK,
			body:   `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas"><cas:authenticationSuccess/></cas:serviceResponse>`,
		},
		{
			name:   "upstream error",
			status: http.StatusBadGateway,
			body:   "upstream failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			verifier := NewVerifierWithCASService(server.URL, "", "", service)
			if got, err := verifier.VerifyCASTicket(t.Context(), "ST-test"); err == nil || got != "" {
				t.Fatalf("VerifyCASTicket() = %q, %v; want an error", got, err)
			}
		})
	}
}

func TestVerifyCASTicketEscapesQueryParameters(t *testing.T) {
	service := "http://ca.csu.edu.cn/personalInfo/personCenter/index.html?tab=" + url.QueryEscape("基本信息")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("service"); got != service {
			t.Errorf("service = %q, want %q", got, service)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas"><cas:authenticationSuccess><cas:user>student</cas:user></cas:authenticationSuccess></cas:serviceResponse>`)
	}))
	defer server.Close()

	verifier := NewVerifierWithCASService(server.URL, "", "", service)
	if _, err := verifier.VerifyCASTicket(t.Context(), "ST-test"); err != nil {
		t.Fatalf("VerifyCASTicket() error = %v", err)
	}
}
