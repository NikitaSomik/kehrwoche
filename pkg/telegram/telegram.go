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

// Command is one entry in the bot's command menu (Telegram's setMyCommands).
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Send posts a Markdown-formatted message to the given Telegram chat using
// client. The bot token is never logged — errors describe the failure without
// including the URL.
func Send(ctx context.Context, client *http.Client, token string, chatID int64, text string) error {
	return sendMessage(ctx, client, token, chatID, text, "Markdown")
}

// SendPlain posts a message with no parse mode, for text that would trip
// Markdown parsing (e.g. command names containing underscores).
func SendPlain(ctx context.Context, client *http.Client, token string, chatID int64, text string) error {
	return sendMessage(ctx, client, token, chatID, text, "")
}

func sendMessage(ctx context.Context, client *http.Client, token string, chatID int64, text, parseMode string) error {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	_, err := call(ctx, client, token, "sendMessage", payload)
	return err
}

// SetCommands replaces the bot's command menu (setMyCommands) for the given
// scope; an empty scope means the default scope. Scope is a BotCommandScope
// type string, e.g. "all_private_chats", "all_group_chats".
func SetCommands(ctx context.Context, client *http.Client, token string, cmds []Command, scope string) error {
	payload := map[string]any{"commands": cmds}
	if scope != "" {
		payload["scope"] = map[string]string{"type": scope}
	}
	_, err := call(ctx, client, token, "setMyCommands", payload)
	return err
}

// GetCommands returns the bot's command menu (getMyCommands) for the given
// scope; an empty scope means the default scope.
func GetCommands(ctx context.Context, client *http.Client, token, scope string) ([]Command, error) {
	payload := map[string]any{}
	if scope != "" {
		payload["scope"] = map[string]string{"type": scope}
	}
	raw, err := call(ctx, client, token, "getMyCommands", payload)
	if err != nil {
		return nil, err
	}
	var out struct {
		Result []Command `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("telegram: decode getMyCommands: %w", err)
	}
	return out.Result, nil
}

// call POSTs a JSON payload to a Bot API method and returns the raw response
// body. Errors never include the bot token.
func call(ctx context.Context, client *http.Client, token, method string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// http.Client wraps transport failures in a *url.Error whose Error()
		// includes the full request URL — and thus the bot token. Unwrap to
		// the underlying error before it reaches logs.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("telegram: %s failed: %w", method, uerr.Err)
		}
		return nil, fmt.Errorf("telegram: %s failed: %w", method, err)
	}
	defer resp.Body.Close()
	// Cap the read: Bot API responses are small JSON, but never trust a
	// response size blindly.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: %s: unexpected status %d: %s", method, resp.StatusCode, bytes.TrimSpace(respBody))
	}
	return respBody, nil
}
