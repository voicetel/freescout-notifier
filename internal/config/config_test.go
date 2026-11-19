package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validConfig() *Config {
	cfg := &Config{
		DBPath: "./test.db",
		FreeScout: FreeScoutConfig{
			DSN: "user:pass@tcp(localhost:3306)/freescout",
			URL: "https://support.example.com/",
		},
		Slack: SlackConfig{
			WebhookURL:    "https://hooks.slack.com/services/X/Y/Z",
			Timeout:       Duration{Duration: 10 * time.Second},
			RetryAttempts: 3,
		},
		PollInterval:        Duration{Duration: 30 * time.Second},
		CheckInterval:       Duration{Duration: 5 * time.Minute},
		LookbackWindow:      Duration{Duration: 2 * time.Hour},
		UnansweredThreshold: Duration{Duration: 2 * time.Hour},
		PendingThreshold:    Duration{Duration: 24 * time.Hour},
		ReminderInterval:    Duration{Duration: 4 * time.Hour},
		MaxNotifications:    50,
		BusinessHours: BusinessHoursConfig{
			Enabled:   true,
			StartHour: 9,
			EndHour:   17,
			Timezone:  "UTC",
			WorkDays:  []time.Weekday{time.Monday, time.Friday},
		},
		RetentionDays: 90,
		LogFormat:     "text",
	}
	return cfg
}

func TestValidateOK(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
	// parseTime is appended and the URL trailing slash trimmed.
	if cfg.FreeScout.DSN != "user:pass@tcp(localhost:3306)/freescout?parseTime=true" {
		t.Errorf("unexpected normalized DSN: %s", cfg.FreeScout.DSN)
	}
	if cfg.FreeScout.URL != "https://support.example.com" {
		t.Errorf("unexpected normalized URL: %s", cfg.FreeScout.URL)
	}
}

func TestValidateKeepsExistingParseTime(t *testing.T) {
	cfg := validConfig()
	cfg.FreeScout.DSN = "user:pass@tcp(h:3306)/db?parseTime=true&loc=UTC"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.FreeScout.DSN != "user:pass@tcp(h:3306)/db?parseTime=true&loc=UTC" {
		t.Errorf("DSN should be untouched: %s", cfg.FreeScout.DSN)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing dsn", func(c *Config) { c.FreeScout.DSN = "" }},
		{"bad dsn", func(c *Config) { c.FreeScout.DSN = "localhost" }},
		{"tcp scheme dsn", func(c *Config) { c.FreeScout.DSN = "tcp://user@localhost/db" }},
		{"missing url", func(c *Config) { c.FreeScout.URL = "" }},
		{"missing webhook", func(c *Config) { c.Slack.WebhookURL = "" }},
		{"poll too fast", func(c *Config) { c.PollInterval = Duration{Duration: time.Second} }},
		{"check too fast", func(c *Config) { c.CheckInterval = Duration{Duration: 10 * time.Second} }},
		{"lookback too small", func(c *Config) { c.LookbackWindow = Duration{Duration: time.Minute} }},
		{"lookback under poll", func(c *Config) {
			c.PollInterval = Duration{Duration: 90 * time.Minute}
			c.CheckInterval = Duration{Duration: 2 * time.Hour}
			c.LookbackWindow = Duration{Duration: 100 * time.Minute}
		}},
		{"negative threshold", func(c *Config) { c.UnansweredThreshold = Duration{Duration: 0} }},
		{"negative reminder", func(c *Config) { c.ReminderInterval = Duration{Duration: -time.Hour} }},
		{"bad hours", func(c *Config) { c.BusinessHours.StartHour = 18 }},
		{"empty workdays", func(c *Config) { c.BusinessHours.WorkDays = nil }},
		{"bad log format", func(c *Config) { c.LogFormat = "yaml" }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := validConfig()
			c.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestValidateDryRunNeedsNoWebhook(t *testing.T) {
	cfg := validConfig()
	cfg.DryRun = true
	cfg.Slack.WebhookURL = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("dry-run should not require a webhook: %v", err)
	}
}

func TestValidateUtilityModesNeedNoURL(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.StatsOnly = true },
		func(c *Config) { c.Cleanup = true },
		func(c *Config) { c.InitDB = true },
	} {
		cfg := validConfig()
		cfg.FreeScout.URL = ""
		cfg.Slack.WebhookURL = ""
		mutate(cfg)
		if err := cfg.Validate(); err != nil {
			t.Errorf("utility mode should not require URL/webhook: %v", err)
		}
	}
}

func TestValidateAppliesDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.MaxNotifications = 0
	cfg.RetentionDays = 0
	cfg.Slack.RetryAttempts = 0
	cfg.Slack.Timeout = Duration{}
	cfg.LogFormat = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MaxNotifications != 50 || cfg.RetentionDays != 90 || cfg.Slack.RetryAttempts != 3 ||
		cfg.Slack.Timeout.Duration != 10*time.Second || cfg.LogFormat != "text" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestValidatePushConfig(t *testing.T) {
	creds := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(creds, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("disabled needs nothing", func(t *testing.T) {
		cfg := validConfig()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("push disabled should not require credentials: %v", err)
		}
	})

	t.Run("enabled requires credentials file", func(t *testing.T) {
		cfg := validConfig()
		cfg.Push.Enabled = true
		cfg.Push.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for missing credentials file")
		}
	})

	t.Run("enabled applies defaults", func(t *testing.T) {
		cfg := validConfig()
		cfg.Push.Enabled = true
		cfg.Push.CredentialsFile = creds
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		if cfg.Push.Topic != "desk-alerts" {
			t.Errorf("topic = %q, want desk-alerts", cfg.Push.Topic)
		}
		if len(cfg.Push.AlertTypes) != 4 {
			t.Errorf("alert_types = %v, want all four types", cfg.Push.AlertTypes)
		}
	})

	t.Run("rejects unknown alert type", func(t *testing.T) {
		cfg := validConfig()
		cfg.Push.Enabled = true
		cfg.Push.CredentialsFile = creds
		cfg.Push.AlertTypes = []string{"new_ticket", "bogus"}
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for unknown alert type")
		}
	})

	t.Run("test-push requires push enabled", func(t *testing.T) {
		cfg := validConfig()
		cfg.TestPush = true
		if err := cfg.Validate(); err == nil {
			t.Error("expected error when push is disabled")
		}
	})

	t.Run("test-push needs no freescout or slack", func(t *testing.T) {
		cfg := validConfig()
		cfg.TestPush = true
		cfg.Push.Enabled = true
		cfg.Push.CredentialsFile = creds
		cfg.FreeScout.DSN = ""
		cfg.FreeScout.URL = ""
		cfg.Slack.WebhookURL = ""
		if err := cfg.Validate(); err != nil {
			t.Errorf("test-push should not require FreeScout/Slack: %v", err)
		}
	})
}

func TestDurationJSON(t *testing.T) {
	var cfg Config
	data := []byte(`{"poll_interval":"45s","check_interval":"10m","lookback_window":"3h"}`)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval.Duration != 45*time.Second {
		t.Errorf("poll_interval = %v, want 45s", cfg.PollInterval.Duration)
	}
	if cfg.CheckInterval.Duration != 10*time.Minute {
		t.Errorf("check_interval = %v, want 10m", cfg.CheckInterval.Duration)
	}
	if cfg.LookbackWindow.Duration != 3*time.Hour {
		t.Errorf("lookback_window = %v, want 3h", cfg.LookbackWindow.Duration)
	}

	out, err := json.Marshal(cfg.PollInterval)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"45s"` {
		t.Errorf("marshaled duration = %s, want \"45s\"", out)
	}

	if err := json.Unmarshal([]byte(`{"poll_interval":"banana"}`), &cfg); err == nil {
		t.Error("expected error for invalid duration string")
	}
}
