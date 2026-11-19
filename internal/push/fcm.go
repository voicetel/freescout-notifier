// Package push delivers mobile alerts to the VoiceTel Desk app via Firebase
// Cloud Messaging (HTTP v1), using our own Firebase project credentials.
package push

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/voicetel/freescout-notifier/internal/config"
)

// DefaultTopic is the FCM topic the mobile app subscribes to.
const DefaultTopic = "desk-alerts"

// Client sends data-only FCM messages to a single topic.
type Client struct {
	messaging *messaging.Client
	topic     string
	types     map[string]bool
}

// NewClient builds a Client from configuration. It performs no network I/O;
// credentials are exchanged for tokens lazily on the first send.
func NewClient(ctx context.Context, cfg config.PushConfig) (*Client, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(cfg.CredentialsFile))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Firebase app: %w", err)
	}
	mc, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Firebase messaging: %w", err)
	}

	types := make(map[string]bool, len(cfg.AlertTypes))
	for _, t := range cfg.AlertTypes {
		types[t] = true
	}
	return &Client{messaging: mc, topic: cfg.Topic, types: types}, nil
}

// ShouldPush reports whether alerts of this type are delivered as pushes.
func (c *Client) ShouldPush(alertType string) bool {
	return c.types[alertType]
}

// Topic returns the FCM topic this client publishes to.
func (c *Client) Topic() string { return c.topic }

// Send delivers a data-only message to the configured topic. Data-only
// messages reach the app's onMessageReceived in foreground and background
// alike, so the app controls rendering and deep links.
func (c *Client) Send(ctx context.Context, title, body, url, alertType string) error {
	msg := &messaging.Message{
		Topic: c.topic,
		Data: map[string]string{
			"title": title,
			"body":  body,
			"url":   url,
			"type":  alertType,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	}
	if _, err := c.messaging.Send(ctx, msg); err != nil {
		return fmt.Errorf("FCM send failed: %w", err)
	}
	return nil
}

// Test sends a verification push; used by --test-push.
func (c *Client) Test(ctx context.Context, appURL string) error {
	return c.Send(ctx,
		"VoiceTel Desk",
		"✅ Push test from freescout-notifier — if you see this, mobile alerts are live",
		appURL, "test")
}
