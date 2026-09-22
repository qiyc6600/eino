package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
)

// weatherBody is a trimmed but realistic wttr.in j1 response: the same field
// shapes the struct reads, with values distinctive enough that a test can prove
// they came from the response rather than from a constant in the package.
const weatherBody = `{
  "current_condition": [{
    "temp_C": "26",
    "FeelsLikeC": "24",
    "humidity": "44",
    "weatherDesc": [{"value": "Smoky haze"}],
    "winddir16Point": "NW",
    "windspeedKmph": "4",
    "observation_time": "02:32 AM"
  }],
  "nearest_area": [{
    "areaName": [{"value": "Beijing"}],
    "country": [{"value": "China"}]
  }]
}`

// stubTransport replaces the HTTP round trip so the tests never touch the
// network: the weather tool is otherwise untestable offline, which is how it
// ended up with no tests at all.
type stubTransport struct {
	status int
	body   string
	err    error
}

func (s stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
	}, nil
}

// stubWeatherClient installs a client whose only transport is the stub. Tests
// must not call t.Parallel(): weatherHTTPClient is package state.
func stubWeatherClient(t *testing.T, s stubTransport) {
	t.Helper()
	prev := weatherHTTPClient
	weatherHTTPClient = &http.Client{Transport: s, Timeout: 5 * time.Second}
	t.Cleanup(func() { weatherHTTPClient = prev })
}

func TestWeather_SuccessUsesUpstreamValues(t *testing.T) {
	stubWeatherClient(t, stubTransport{status: http.StatusOK, body: weatherBody})

	result := executeWeather(nil, `{"city":"Beijing"}`)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	// Every one of these values exists only in the stubbed response.
	for _, want := range []string{"Beijing", "China", "26", "24", "44", "Smoky haze", "NW", "02:32 AM"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("content is missing %q from the upstream response:\n%s", want, result.Content)
		}
	}
	if got := result.Metadata["status"]; got != "success" {
		t.Errorf("status = %v, want success", got)
	}
	if got := result.Metadata["source"]; got != "wttr.in" {
		t.Errorf("source = %v, want wttr.in", got)
	}
}

// TestWeather_FailuresNeverFabricateWeather is the guard on this tool's honesty:
// when the upstream lookup fails, the result must be an error with no weather in
// it. Substituting invented numbers was the previous behaviour, and it was
// invisible to callers because it came back as a success.
func TestWeather_FailuresNeverFabricateWeather(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name       string
		stub       stubTransport
		identity   *auth.ToolIdentity
		wantStatus string
	}{
		{
			name:       "network failure",
			stub:       stubTransport{err: errors.New("dial tcp: connection refused")},
			wantStatus: "business_error",
		},
		{
			name:       "upstream 500",
			stub:       stubTransport{status: http.StatusInternalServerError, body: "upstream is down"},
			wantStatus: "business_error",
		},
		{
			name:       "unparseable body",
			stub:       stubTransport{status: http.StatusOK, body: "<html>not json</html>"},
			wantStatus: "business_error",
		},
		{
			name:       "no current conditions",
			stub:       stubTransport{status: http.StatusOK, body: `{"current_condition":[]}`},
			wantStatus: "business_error",
		},
		{
			// A cancelled request means the caller no longer wants an answer, so
			// this is a system error and aborts the run — it must not be softened
			// into an answerable result.
			name:       "cancelled context",
			stub:       stubTransport{err: context.Canceled},
			identity:   &auth.ToolIdentity{Context: cancelled},
			wantStatus: "system_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubWeatherClient(t, tc.stub)

			result := executeWeather(tc.identity, `{"city":"Beijing"}`)
			if result.Error == "" {
				t.Fatalf("a failed lookup must report an error, got content:\n%s", result.Content)
			}
			if got := result.Metadata["status"]; got != tc.wantStatus {
				t.Errorf("status = %v, want %s", got, tc.wantStatus)
			}
			// The old fallback filled Content with invented values. Anything that
			// looks like an observation here means fabrication came back.
			for _, forbidden := range []string{"°C", "温度", "湿度", "离线"} {
				if strings.Contains(result.Content, forbidden) {
					t.Errorf("failed lookup produced weather content (%q):\n%s", forbidden, result.Content)
				}
			}
			if src, ok := result.Metadata["source"].(string); ok && strings.Contains(src, "fallback") {
				t.Errorf("failed lookup reported a fallback source: %v", src)
			}
		})
	}
}

// The error text is what the model reads, so it has to name the cause.
func TestWeather_FailureNamesTheCityAndCause(t *testing.T) {
	stubWeatherClient(t, stubTransport{err: errors.New("dial tcp: connection refused")})

	result := executeWeather(nil, `{"city":"Wuhan"}`)
	if !strings.Contains(result.Error, "Wuhan") {
		t.Errorf("error does not name the city: %s", result.Error)
	}
	if !strings.Contains(result.Error, "connection refused") {
		t.Errorf("error does not carry the upstream cause: %s", result.Error)
	}
}

func TestWeather_ArgumentValidation(t *testing.T) {
	// No stub installed: these must fail before any request is made.
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing city", `{}`, "city is required"},
		{"empty city", `{"city":""}`, "city is required"},
		{"malformed json", `{"city":`, "invalid arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := executeWeather(nil, tc.args)
			if result.Metadata["status"] != "business_error" {
				t.Errorf("status = %v, want business_error", result.Metadata["status"])
			}
			if !strings.Contains(result.Error, tc.want) {
				t.Errorf("error = %q, want it to contain %q", result.Error, tc.want)
			}
		})
	}
}

// A city the upstream API cannot resolve must also be an error, not a made-up
// reading for a place that does not exist.
func TestWeather_UnknownCityIsAnError(t *testing.T) {
	stubWeatherClient(t, stubTransport{status: http.StatusOK, body: `{"current_condition":[]}`})

	result := executeWeather(nil, `{"city":"Nowhereville"}`)
	if result.Error == "" {
		t.Fatalf("unknown city must be an error, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Error, "Nowhereville") {
		t.Errorf("error does not name the city: %s", result.Error)
	}
}
