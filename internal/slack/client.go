// Package slack delivers messages to a Slack incoming webhook.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
)

// Client posts messages to a single Slack incoming webhook.
type Client struct {
	webhookURL    string
	httpClient    *http.Client
	retryAttempts int
}

// Message is the incoming-webhook payload.
type Message struct {
	Text string `json:"text"`
}

// NewClient builds a client from configuration, applying sane defaults.
func NewClient(cfg config.SlackConfig) *Client {
	timeout := cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	attempts := cfg.RetryAttempts
	if attempts <= 0 {
		attempts = 3
	}
	return &Client{
		webhookURL:    cfg.WebhookURL,
		httpClient:    &http.Client{Timeout: timeout},
		retryAttempts: attempts,
	}
}

// SendMessage posts a text message, retrying transient failures (network
// errors, 429 and 5xx) with exponential backoff. Permanent failures (other
// 4xx) are returned immediately.
func (c *Client) SendMessage(ctx context.Context, text string) error {
	payload, err := json.Marshal(Message{Text: text})
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < c.retryAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		retry, err := c.post(ctx, payload)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return fmt.Errorf("slack delivery failed after %d attempt(s): %w", c.retryAttempts, lastErr)
}

// post performs a single delivery attempt. It reports whether the failure is
// worth retrying.
func (c *Client) post(ctx context.Context, payload []byte) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusOK {
		return false, nil
	}

	retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retry, fmt.Errorf("slack webhook returned status %d", resp.StatusCode)
}

// TestWebhook sends a test message; used by --check-connections.
func TestWebhook(ctx context.Context, cfg config.SlackConfig) error {
	return NewClient(cfg).SendMessage(ctx, "🔧 FreeScout Notifier test message - connection successful!")
}
