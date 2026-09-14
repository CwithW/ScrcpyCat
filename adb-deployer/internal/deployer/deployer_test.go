package deployer

import "testing"

func TestLoopbackSignalingPort(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"ws://127.0.0.1:8443/register_agent", true},
		{"wss://localhost/register_agent", true},
		{"wss://control.example/register_agent", false},
	}
	for _, test := range tests {
		_, got, err := loopbackSignalingPort(test.url)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("loopback(%q) = %v, want %v", test.url, got, test.want)
		}
	}
}
