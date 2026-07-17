package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Send posts a Markdown message to the given Telegram chat using client.
// The bot token is never logged — errors describe the failure without including the URL.
func Send(ctx context.Context, client *http.Client, token string, chatID int64, text string) error {
	body, err := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// http.Client wraps transport failures in a *url.Error whose Error()
		// includes the full request URL — and thus the bot token. Unwrap to
		// the underlying error before it reaches logs.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("telegram: request failed: %w", uerr.Err)
		}
		return fmt.Errorf("telegram: request failed: %w", err)
	}
	defer resp.Body.Close()
	// Cap the read: Telegram error bodies are small JSON, but never trust a
	// response size blindly.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}
	return nil
}
