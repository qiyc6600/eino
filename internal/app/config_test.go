package app

import (
	"testing"
	"time"
)

// TestGetEnvInt pins the "invalid input falls back" rule that every helper in
// config.go follows.
//
// The previous hand-rolled loop accepted digits and wrapped silently on
// overflow, which is the worst version of this failure: a huge typo became a
// small plausible number rather than the documented default.
func TestGetEnvInt(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", 5},
		{"30", 30},
		{"007", 7},
		{" 30 ", 30}, // a trailing space in a .env file must not silently fall back
		{"0", 0},
		// Rejected: a sign, a decimal point, trailing text, overflow.
		{"-5", 5},
		{"1.2", 5},
		{"30x", 5},
		{"abc", 5},
		{"0x10", 5},
		{"18446744073709551617", 5}, // wrapped to 1 before the fix
		{"99999999999999999999", 5}, // wrapped to 7766279631452241919
	}
	for _, c := range cases {
		t.Setenv("TEST_INT", c.value)
		if got := getEnvInt("TEST_INT", 5); got != c.want {
			t.Errorf("getEnvInt(%q) = %d, want %d", c.value, got, c.want)
		}
	}
}

// TestGetEnvFloat covers the multi-dot case the old parser silently accepted.
func TestGetEnvFloat(t *testing.T) {
	cases := []struct {
		value string
		want  float64
	}{
		{"", 0.8},
		{"0.8", 0.8},
		{".5", 0.5},
		{"1.", 1.0},
		{"1", 1.0},
		{" 0.65 ", 0.65},
		{"0", 0},
		{"1e-3", 0.001}, // valid float spelling, accepted
		// Rejected: extra decimal points, text, non-finite values, negatives.
		{"1.2.3", 0.8}, // read as 1.23 before the fix
		{"0.1.5", 0.8}, // read as 0.15
		{"1..5", 0.8},  // read as 1.5
		{"abc", 0.8},
		{"0.8x", 0.8},
		{"NaN", 0.8},
		{"Inf", 0.8},
		{"-1", 0.8},
		{"-0.5", 0.8},
	}
	for _, c := range cases {
		t.Setenv("TEST_FLOAT", c.value)
		if got := getEnvFloat("TEST_FLOAT", 0.8); got != c.want {
			t.Errorf("getEnvFloat(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

// TestGetEnvDurationAndBool cover the two helpers that already followed the rule,
// so all four are pinned together and a future rewrite has to keep the contract.
func TestGetEnvDurationAndBool(t *testing.T) {
	durations := []struct {
		value string
		want  time.Duration
	}{
		{"", 30 * time.Minute},
		{"2h", 2 * time.Hour},
		{" 90s ", 90 * time.Second},
		// Rejected: unparseable, zero and negative all fall back rather than
		// disabling a timeout by accident.
		{"abc", 30 * time.Minute},
		{"0", 30 * time.Minute},
		{"-1h", 30 * time.Minute},
	}
	for _, c := range durations {
		t.Setenv("TEST_DUR", c.value)
		if got := getEnvDuration("TEST_DUR", 30*time.Minute); got != c.want {
			t.Errorf("getEnvDuration(%q) = %v, want %v", c.value, got, c.want)
		}
	}

	bools := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"1", true},
		{"TRUE", true},
		{" Yes ", true},
		{"on", true},
		{"0", false},
		{"false", false},
		{"OFF", false},
		// Anything else falls back rather than guessing — a typo must not flip a
		// security-relevant switch like SESSION_COOKIE_SECURE.
		{"maybe", true},
		{"2", true},
		{"tru", true},
	}
	for _, c := range bools {
		t.Setenv("TEST_BOOL", c.value)
		if got := getEnvBool("TEST_BOOL", true); got != c.want {
			t.Errorf("getEnvBool(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

// TestGetEnvList pins the list parser, including that an unset or empty variable
// yields the fallback rather than an empty slice.
func TestGetEnvList(t *testing.T) {
	cases := []struct {
		value string
		want  []string
	}{
		{"", []string{"fallback"}},
		{"   ", []string{"fallback"}},
		{"a", []string{"a"}},
		{"a,b", []string{"a", "b"}},
		{" a , b ", []string{"a", "b"}},
		{"a,,b", []string{"a", "b"}},  // empty entries dropped
		{"a, ,b", []string{"a", "b"}}, // so is whitespace-only
		{"a,", []string{"a"}},
	}
	for _, c := range cases {
		t.Setenv("TEST_LIST", c.value)
		got := getEnvList("TEST_LIST", "fallback")
		if len(got) != len(c.want) {
			t.Errorf("getEnvList(%q) = %v, want %v", c.value, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("getEnvList(%q) = %v, want %v", c.value, got, c.want)
				break
			}
		}
	}
}
