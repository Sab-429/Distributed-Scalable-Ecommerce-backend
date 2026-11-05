package telemetry

import "testing"

// WithEndpointURL stores the parsed path verbatim, so an empty path suppresses
// the exporter's default "/v1/<signal>" suffix and every export is POSTed to
// the collector root (404). signalEndpoint must always produce a full URL.
func TestSignalEndpoint(t *testing.T) {
	cases := []struct {
		base string
		sig  string
		want string
	}{
		{"http://localhost:4318", "/v1/logs", "http://localhost:4318/v1/logs"},
		{"http://localhost:4318/", "/v1/logs", "http://localhost:4318/v1/logs"},
		{"http://collector:4318", "/v1/metrics", "http://collector:4318/v1/metrics"},
		{"https://otlp.example.com:4318", "/v1/traces", "https://otlp.example.com:4318/v1/traces"},
		{"http://localhost:4318/otlp", "/v1/logs", "http://localhost:4318/otlp/v1/logs"},
		{"", "/v1/logs", "/v1/logs"},
	}
	for _, c := range cases {
		if got := signalEndpoint(c.base, c.sig); got != c.want {
			t.Errorf("signalEndpoint(%q, %q) = %q, want %q", c.base, c.sig, got, c.want)
		}
	}
}
