package config

import (
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"MODE":            "live",
		"T212_API_KEY":    "t212-live-abc123",
		"T212_API_SECRET": "s3cr3t-xyz789",
		"MQTT_BROKER_URL": "mqtt://localhost:1883",
	}
}

func TestModeResolvesBaseURL(t *testing.T) {
	cases := []struct {
		name string
		mode string
		want string
	}{
		{"live", "live", "https://live.trading212.com/api/v0"},
		{"demo", "demo", "https://demo.trading212.com/api/v0"},
		{"mock", "mock", "http://localhost:8090/api/v0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			env["MODE"] = tc.mode
			setEnv(t, env)
			c, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if c.APIBaseURL != tc.want {
				t.Errorf("APIBaseURL = %q, want %q", c.APIBaseURL, tc.want)
			}
		})
	}
}

func TestModeMockCustomURL(t *testing.T) {
	env := baseEnv()
	env["MODE"] = "mock"
	env["MOCK_URL"] = "http://127.0.0.1:9999"
	setEnv(t, env)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.APIBaseURL != "http://127.0.0.1:9999/api/v0" {
		t.Errorf("APIBaseURL = %q", c.APIBaseURL)
	}
}

func TestModeMockSuppliesDummyCredentials(t *testing.T) {
	setEnv(t, map[string]string{
		"MODE":            "mock",
		"MQTT_BROKER_URL": "mqtt://localhost:1883",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.APIKey == "" {
		t.Error("APIKey should be a dummy value in mock mode")
	}
}

func TestAPIKeyRequiredInLiveAndDemo(t *testing.T) {
	for _, mode := range []string{"live", "demo"} {
		t.Run(mode, func(t *testing.T) {
			setEnv(t, map[string]string{
				"MODE":            mode,
				"MQTT_BROKER_URL": "mqtt://localhost:1883",
			})
			_, err := Load()
			if err == nil {
				t.Fatal("expected an error when T212_API_KEY is unset")
			}
			if !strings.Contains(err.Error(), "T212_API_KEY") {
				t.Errorf("error = %v, want it to mention T212_API_KEY", err)
			}
		})
	}
}

func TestBrokerURLRequired(t *testing.T) {
	setEnv(t, map[string]string{"MODE": "mock"})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MQTT_BROKER_URL") {
		t.Fatalf("error = %v, want it to mention MQTT_BROKER_URL", err)
	}
}

func TestBrokerURLRequiresScheme(t *testing.T) {
	env := baseEnv()
	env["MQTT_BROKER_URL"] = "just-text"
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MQTT_BROKER_URL") {
		t.Fatalf("error = %v, want it to mention MQTT_BROKER_URL", err)
	}
}

func TestInvalidModeIsRejected(t *testing.T) {
	env := baseEnv()
	env["MODE"] = "staging"
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "MODE") {
		t.Fatalf("error = %v, want it to mention MODE", err)
	}
}

func TestPollIntervalDefaultAndFloor(t *testing.T) {
	setEnv(t, baseEnv())
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m", c.PollInterval)
	}

	env := baseEnv()
	env["POLL_INTERVAL"] = "30s"
	setEnv(t, env)
	if _, err := Load(); err == nil {
		t.Fatal("expected 30s to be rejected by the 1m floor")
	}
}

func TestPollMaxRetriesRejectsNegative(t *testing.T) {
	env := baseEnv()
	env["POLL_MAX_RETRIES"] = "-1"
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "POLL_MAX_RETRIES") {
		t.Fatalf("error = %v, want it to mention POLL_MAX_RETRIES", err)
	}
}

func TestReadyFailureThresholdRejectsBelowOne(t *testing.T) {
	env := baseEnv()
	env["READY_FAILURE_THRESHOLD"] = "0"
	setEnv(t, env)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "READY_FAILURE_THRESHOLD") {
		t.Fatalf("error = %v, want it to mention READY_FAILURE_THRESHOLD", err)
	}
}

func TestInvalidIntegerIsRejected(t *testing.T) {
	for _, key := range []string{"READY_FAILURE_THRESHOLD", "POLL_MAX_RETRIES"} {
		t.Run(key, func(t *testing.T) {
			env := baseEnv()
			env[key] = "not-a-number"
			setEnv(t, env)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("error = %v, want it to mention %s", err, key)
			}
		})
	}
}

func TestErrorsAreCollected(t *testing.T) {
	setEnv(t, map[string]string{"MODE": "banana"})
	_, err := Load()
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "MODE") || !strings.Contains(msg, "MQTT_BROKER_URL") {
		t.Errorf("error = %q, want both MODE and MQTT_BROKER_URL reported", msg)
	}
}

func TestStringRedactsSecrets(t *testing.T) {
	env := baseEnv()
	env["MQTT_PASSWORD"] = "hunter2"
	setEnv(t, env)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	s := c.String()
	for _, secret := range []string{"t212-live-abc123", "s3cr3t-xyz789", "hunter2"} {
		if strings.Contains(s, secret) {
			t.Errorf("String() = %q, must not contain %q", s, secret)
		}
	}
}
