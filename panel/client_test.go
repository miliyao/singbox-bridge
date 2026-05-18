package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestEndpointEncodesQueryValues(t *testing.T) {
	client := NewClient("https://panel.example.com/", "a+b&c=d", 42)

	got := client.endpoint("/api/v1/server/UniProxy/config")
	want := "https://panel.example.com/api/v1/server/UniProxy/config?node_id=42&node_type=vless&token=a%2Bb%26c%3Dd"

	if got != want {
		t.Fatalf("endpoint() = %q, want %q", got, want)
	}
}

func TestGetNodeConfigDecodesStringIntervals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Etag", `"config-v1"`)
		_, _ = w.Write([]byte(`{"base_config":{"push_interval":"120","pull_interval":"60"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	config, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("GetNodeConfig returned error: %v", err)
	}
	if int(config.BaseConfig.PushInterval) != 120 || int(config.BaseConfig.PullInterval) != 60 {
		t.Fatalf("unexpected intervals: %#v", config.BaseConfig)
	}

	second, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("cached GetNodeConfig returned error: %v", err)
	}
	if int(second.BaseConfig.PushInterval) != 120 || int(second.BaseConfig.PullInterval) != 60 {
		t.Fatalf("unexpected cached intervals: %#v", second.BaseConfig)
	}
}

func TestGetNodeConfigDecodesShortIDList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tls_settings":{"short_id":["abcd","ef01"]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	config, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("GetNodeConfig returned error: %v", err)
	}

	want := []string{"abcd", "ef01"}
	if !reflect.DeepEqual(config.TLSSettings.ShortIDList(), want) {
		t.Fatalf("unexpected short ids: got %#v want %#v", config.TLSSettings.ShortIDList(), want)
	}
	if config.TLSSettings.ShortID != "abcd" {
		t.Fatalf("ShortID = %q, want abcd", config.TLSSettings.ShortID)
	}
}

func TestGetNodeConfigDecodesCommaSeparatedShortIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tls_settings":{"short_id":"abcd, ef01"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	config, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("GetNodeConfig returned error: %v", err)
	}

	want := []string{"abcd", "ef01"}
	if !reflect.DeepEqual(config.TLSSettings.ShortIDList(), want) {
		t.Fatalf("unexpected short ids: got %#v want %#v", config.TLSSettings.ShortIDList(), want)
	}
}

func TestGetNodeConfigUsesCachedValueOnNotModified(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&calls, 1)
		if attempt == 1 {
			w.Header().Set("Etag", `"config-v1"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"server_port":443,"tls_settings":{"server_name":"example.com","private_key":"key"}}`))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != `"config-v1"` {
			t.Fatalf("If-None-Match = %q, want config etag", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	first, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("first GetNodeConfig returned error: %v", err)
	}
	if first.TLSSettings.ServerName != "example.com" {
		t.Fatalf("unexpected first config: %#v", first)
	}

	second, err := client.GetNodeConfig()
	if err != nil {
		t.Fatalf("second GetNodeConfig returned error: %v", err)
	}
	if second.TLSSettings.ServerName != "example.com" {
		t.Fatalf("unexpected cached config: %#v", second)
	}
}

func TestGetUsersRetriesOnServerError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&calls, 1)
		if attempt < 3 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"id":1,"uuid":"u1","speed_limit":0}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	users, err := client.GetUsers()
	if err != nil {
		t.Fatalf("GetUsers returned error: %v", err)
	}
	if len(users) != 1 || users[0].ID != 1 {
		t.Fatalf("unexpected users: %#v", users)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestGetUsersUsesETagCacheOnNotModified(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("node_type") != "vless" {
			t.Fatalf("expected node_type=vless, got %q", r.URL.RawQuery)
		}
		if attempt == 1 {
			w.Header().Set("Etag", `"users-v1"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"users":[{"id":1,"uuid":"u1","speed_limit":0}]}`))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != `"users-v1"` {
			t.Fatalf("If-None-Match = %q, want users-v1 etag", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	first, err := client.GetUsers()
	if err != nil {
		t.Fatalf("first GetUsers returned error: %v", err)
	}
	first[0].ID = 99

	second, err := client.GetUsers()
	if err != nil {
		t.Fatalf("second GetUsers returned error: %v", err)
	}
	if len(second) != 1 || second[0].ID != 1 {
		t.Fatalf("expected cached users to be isolated from caller mutation, got %#v", second)
	}
}

func TestGetUsersAcceptsTopLevelArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"uuid":"u1","speed_limit":0}]`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	users, err := client.GetUsers()
	if err != nil {
		t.Fatalf("GetUsers returned error: %v", err)
	}
	if len(users) != 1 || users[0].ID != 1 {
		t.Fatalf("unexpected users: %#v", users)
	}
}

func TestGetUsersDoesNotRetryOnClientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	if _, err := client.GetUsers(); err == nil {
		t.Fatal("expected GetUsers to fail on client error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestPushTrafficRetriesOnTooManyRequests(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&calls, 1)
		if attempt == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	err := client.PushTraffic([]TrafficData{{UID: 1, Upload: 10, Download: 20}})
	if err != nil {
		t.Fatalf("PushTraffic returned error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestGetUserAliveDecodesDirectMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"1":2,"7":3}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	alive, err := client.GetUserAlive()
	if err != nil {
		t.Fatalf("GetUserAlive returned error: %v", err)
	}

	want := AliveList{
		1: {"remote-1-0", "remote-1-1"},
		7: {"remote-7-0", "remote-7-1", "remote-7-2"},
	}
	if !reflect.DeepEqual(alive, want) {
		t.Fatalf("unexpected alive map: got %#v want %#v", alive, want)
	}
}

func TestGetUserAliveDecodesWrappedMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"alive":{"1":2,"7":3}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	alive, err := client.GetUserAlive()
	if err != nil {
		t.Fatalf("GetUserAlive returned error: %v", err)
	}

	want := AliveList{
		1: {"remote-1-0", "remote-1-1"},
		7: {"remote-7-0", "remote-7-1", "remote-7-2"},
	}
	if !reflect.DeepEqual(alive, want) {
		t.Fatalf("unexpected alive map: got %#v want %#v", alive, want)
	}
}

func TestGetUserAliveDecodesIPLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"1":["1.1.1.1","2.2.2.2"],"7":["3.3.3.3"]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	alive, err := client.GetUserAlive()
	if err != nil {
		t.Fatalf("GetUserAlive returned error: %v", err)
	}

	want := AliveList{1: {"1.1.1.1", "2.2.2.2"}, 7: {"3.3.3.3"}}
	if !reflect.DeepEqual(alive, want) {
		t.Fatalf("unexpected alive map: got %#v want %#v", alive, want)
	}
}

func TestSendAliveEncodesUidToIPListMap(t *testing.T) {
	var got map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	err := client.SendAlive(map[int][]string{
		2: {"1.1.1.1", "2.2.2.2"},
		5: {"3.3.3.3"},
	})
	if err != nil {
		t.Fatalf("SendAlive returned error: %v", err)
	}

	want := map[string][]string{
		"2": {"1.1.1.1", "2.2.2.2"},
		"5": {"3.3.3.3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected alive payload: got %#v want %#v", got, want)
	}
}

func TestSendAliveEncodesEmptyMapInsteadOfNull(t *testing.T) {
	var got map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 7)
	if err := client.SendAlive(nil); err != nil {
		t.Fatalf("SendAlive returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected decoded body to be an empty object, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty alive payload, got %#v", got)
	}
}
