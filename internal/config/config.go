// Package config binds and validates the service configuration from environment
// variables. See docs/specs/05-config.md.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gsdevme/trading212-mqtt/internal/trading212"
)

// MinPollInterval is the floor for POLL_INTERVAL. Rate limits are enforced per
// account, so an over-eager interval would throttle every tool using the same
// credentials, not just this one.
const MinPollInterval = time.Minute

// Base URLs for the two real environments. See docs/specs/01-trading212-api.md.
const (
	liveBaseURL = "https://live.trading212.com/api/v0"
	demoBaseURL = "https://demo.trading212.com/api/v0"
	apiPath     = "/api/v0"
)

// defaultMockURL is the mock target used when MODE=mock and MOCK_URL is unset.
// It must match the mock command's default --addr (see internal/cmd/mock.go).
const defaultMockURL = "http://localhost:8090"

// Config is the fully-parsed, validated service configuration.
type Config struct {
	// API
	Mode       string // "live" | "demo" | "mock"
	APIBaseURL string // fully resolved, including /api/v0
	APIKey     string
	APISecret  string

	// Positions
	Tickers   string               // raw TICKERS value, kept for logging
	Whitelist trading212.Whitelist // parsed form used by the publisher

	// Polling
	PollInterval          time.Duration
	PollMaxRetries        int
	ReadyFailureThreshold int

	// MQTT / HA
	MQTTBrokerURL   string
	MQTTUsername    string
	MQTTPassword    string
	MQTTClientID    string
	TopicPrefix     string
	DiscoveryPrefix string

	// HTTP & logging
	HTTPAddr  string
	LogLevel  string
	LogFormat string
}

// Load reads configuration from the environment, applies defaults and validates.
// Every validation failure is collected so a misconfigured deployment reports all
// of its problems in one run.
func Load() (*Config, error) {
	c := &Config{
		APIKey:          os.Getenv("T212_API_KEY"),
		APISecret:       os.Getenv("T212_API_SECRET"),
		Tickers:         os.Getenv("TICKERS"),
		MQTTBrokerURL:   os.Getenv("MQTT_BROKER_URL"),
		MQTTUsername:    os.Getenv("MQTT_USERNAME"),
		MQTTPassword:    os.Getenv("MQTT_PASSWORD"),
		MQTTClientID:    getEnv("MQTT_CLIENT_ID", "trading212-mqtt"),
		TopicPrefix:     getEnv("TOPIC_PREFIX", "trading212"),
		DiscoveryPrefix: getEnv("DISCOVERY_PREFIX", "homeassistant"),
		HTTPAddr:        getEnv("HTTP_ADDR", ":8080"),
		LogLevel:        strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:       strings.ToLower(getEnv("LOG_FORMAT", "json")),
	}
	c.Whitelist = trading212.ParseWhitelist(c.Tickers)

	var errs []error

	c.Mode = strings.ToLower(getEnv("MODE", "live"))
	switch c.Mode {
	case "live":
		c.APIBaseURL = liveBaseURL
	case "demo":
		c.APIBaseURL = demoBaseURL
	case "mock":
		c.APIBaseURL = strings.TrimRight(getEnv("MOCK_URL", defaultMockURL), "/") + apiPath
		// The mock ignores credentials; supply dummies so the client accepts them.
		if c.APIKey == "" {
			c.APIKey = "mock"
		}
		if c.APISecret == "" {
			c.APISecret = "mock"
		}
	default:
		errs = append(errs, fmt.Errorf("MODE must be live, demo or mock, got %q", c.Mode))
	}

	if c.Mode == "live" || c.Mode == "demo" {
		if c.APIKey == "" {
			errs = append(errs, errors.New("T212_API_KEY is required"))
		}
	}

	if c.MQTTBrokerURL == "" {
		errs = append(errs, errors.New("MQTT_BROKER_URL is required"))
	} else if u, err := url.Parse(c.MQTTBrokerURL); err != nil {
		errs = append(errs, fmt.Errorf("MQTT_BROKER_URL is invalid: %w", err))
	} else if u.Scheme == "" {
		errs = append(errs, fmt.Errorf("MQTT_BROKER_URL must include a scheme, e.g. mqtt://host:1883, got %q", c.MQTTBrokerURL))
	}

	interval, err := parseDuration("POLL_INTERVAL", 5*time.Minute)
	if err != nil {
		errs = append(errs, err)
	} else if interval < MinPollInterval {
		errs = append(errs, fmt.Errorf("POLL_INTERVAL %s is below the %s floor", interval, MinPollInterval))
	}
	c.PollInterval = interval

	readyFailureThreshold, err := getInt("READY_FAILURE_THRESHOLD", 3)
	if err != nil {
		errs = append(errs, err)
	} else if readyFailureThreshold < 1 {
		errs = append(errs, errors.New("READY_FAILURE_THRESHOLD must be >= 1"))
	}
	c.ReadyFailureThreshold = readyFailureThreshold

	pollMaxRetries, err := getInt("POLL_MAX_RETRIES", 3)
	if err != nil {
		errs = append(errs, err)
	} else if pollMaxRetries < 0 {
		errs = append(errs, errors.New("POLL_MAX_RETRIES must be >= 0"))
	}
	c.PollMaxRetries = pollMaxRetries

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// String renders the config with secrets redacted (safe to log).
func (c *Config) String() string {
	return fmt.Sprintf("Config{mode=%s base=%s key=%s secret=%s tickers=%q poll=%s retries=%d "+
		"readyThreshold=%d broker=%s mqttPassword=%s topicPrefix=%s discoveryPrefix=%s http=%s log=%s/%s}",
		c.Mode, c.APIBaseURL, redact(c.APIKey), redact(c.APISecret), c.Tickers,
		c.PollInterval, c.PollMaxRetries, c.ReadyFailureThreshold,
		c.MQTTBrokerURL, redact(c.MQTTPassword), c.TopicPrefix, c.DiscoveryPrefix, c.HTTPAddr, c.LogLevel, c.LogFormat)
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid integer: %w", key, err)
	}
	return n, nil
}

func parseDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration: %w", key, err)
	}
	return d, nil
}
