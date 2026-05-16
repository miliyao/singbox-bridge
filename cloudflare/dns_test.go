package cloudflare

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterCreatesRecordWhenMissing(t *testing.T) {
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		switch requestCount {
		case 1:
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(apiEnvelope[[]dnsRecord]{
				Success: true,
				Result:  []dnsRecord{},
			})
		case 2:
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(apiEnvelope[dnsRecord]{
				Success: true,
				Result:  dnsRecord{ID: "rec-1", Content: "1.2.3.4"},
			})
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()

	manager := NewDNSManager("token", "zone-1", "node.example.com")
	manager.baseURL = server.URL

	if err := manager.Register("1.2.3.4"); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if manager.recordID != "rec-1" {
		t.Fatalf("expected record ID rec-1, got %q", manager.recordID)
	}
}

func TestRegisterUpdatesExistingRecordWhenIPChanges(t *testing.T) {
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		switch requestCount {
		case 1:
			_ = json.NewEncoder(w).Encode(apiEnvelope[[]dnsRecord]{
				Success: true,
				Result: []dnsRecord{
					{ID: "rec-9", Content: "5.5.5.5"},
				},
			})
		case 2:
			if r.Method != http.MethodPut {
				t.Fatalf("expected PUT, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(apiEnvelope[dnsRecord]{
				Success: true,
				Result:  dnsRecord{ID: "rec-9", Content: "1.2.3.4"},
			})
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()

	manager := NewDNSManager("token", "zone-1", "node.example.com")
	manager.baseURL = server.URL

	if err := manager.Register("1.2.3.4"); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if manager.recordID != "rec-9" {
		t.Fatalf("expected record ID rec-9, got %q", manager.recordID)
	}
}

func TestRegisterSkipsUpdateWhenExistingRecordMatches(t *testing.T) {
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_ = json.NewEncoder(w).Encode(apiEnvelope[[]dnsRecord]{
			Success: true,
			Result: []dnsRecord{
				{ID: "rec-7", Content: "1.2.3.4"},
			},
		})
	}))
	defer server.Close()

	manager := NewDNSManager("token", "zone-1", "node.example.com")
	manager.baseURL = server.URL

	if err := manager.Register("1.2.3.4"); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected 1 request, got %d", requestCount)
	}
}

func TestDeregisterFindsRecordWhenIDMissing(t *testing.T) {
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		switch requestCount {
		case 1:
			_ = json.NewEncoder(w).Encode(apiEnvelope[[]dnsRecord]{
				Success: true,
				Result: []dnsRecord{
					{ID: "rec-4", Content: "1.2.3.4"},
				},
			})
		case 2:
			if r.Method != http.MethodDelete {
				t.Fatalf("expected DELETE, got %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(apiEnvelope[struct{}]{
				Success: true,
				Result:  struct{}{},
			})
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()

	manager := NewDNSManager("token", "zone-1", "node.example.com")
	manager.baseURL = server.URL

	if err := manager.Deregister(); err != nil {
		t.Fatalf("Deregister returned error: %v", err)
	}
	if manager.recordID != "" {
		t.Fatalf("expected record ID to be cleared, got %q", manager.recordID)
	}
}

func TestNormalizePublicIPRejectsInvalidInput(t *testing.T) {
	if _, err := normalizePublicIP("not-an-ip"); err == nil {
		t.Fatal("expected invalid IP error")
	}
}
