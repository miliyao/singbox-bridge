package panel

import "testing"

func TestEndpointEncodesQueryValues(t *testing.T) {
	client := NewClient("https://panel.example.com/", "a+b&c=d", 42)

	got := client.endpoint("/api/v1/server/UniProxy/config")
	want := "https://panel.example.com/api/v1/server/UniProxy/config?node_id=42&token=a%2Bb%26c%3Dd"

	if got != want {
		t.Fatalf("endpoint() = %q, want %q", got, want)
	}
}
