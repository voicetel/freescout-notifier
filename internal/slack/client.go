package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/voicetel/freescout-notifier/internal/config"
)

type Client struct {
	webhookURL    string
	httpClient    *http.Client
	retryAttempts int
}

type Message struct {
	Text string `json:"text"`
}

func NewClient(cfg config.SlackConfig) *Client {
	return &Client{
		webhookURL: cfg.WebhookURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		retryAttempts: cfg.RetryAttempts,
	}
}

func (c *Client) SendMessage(text string) error {
	message := Message{Text: text}
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < c.retryAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			time.Sleep(time.Duration(attempt*attempt) * time.Second)
		}

		req, err := http.NewRequest("POST", c.webhookURL, bytes.NewBuffer(payload))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		lastErr = fmt.Errorf("slack webhook returned status %d", resp.StatusCode)
	}

	return fmt.Errorf("failed after %d attempts: %w", c.retryAttempts, lastErr)
}
