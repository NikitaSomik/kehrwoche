package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// The methods below serve the onboarding: inline keyboards and the callbacks
// they produce, membership checks, join requests, invite links and member
// tags. Like Send, each goes through call and so keeps the token out of
// errors.

// Button is one inline keyboard button. Data comes back verbatim in
// CallbackQuery.Data when it is pressed; Telegram caps it at 64 bytes.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// SendKeyboard posts a plain-text message with an inline keyboard, one slice
// of buttons per row.
func SendKeyboard(ctx context.Context, client *http.Client, token string, chatID int64, text string, rows [][]Button) error {
	_, err := call(ctx, client, token, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": rows},
	})
	return err
}

// EditText replaces the text of a message the bot sent, dropping its inline
// keyboard — the way a pressed button is retired so it can't be pressed again.
func EditText(ctx context.Context, client *http.Client, token string, chatID int64, messageID int, text string) error {
	_, err := call(ctx, client, token, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	})
	return err
}

// AnswerCallback acknowledges a button press, which stops the client's
// spinner. A non-empty text is shown to the presser as a short notice.
func AnswerCallback(ctx context.Context, client *http.Client, token, callbackID, text string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	_, err := call(ctx, client, token, "answerCallbackQuery", payload)
	return err
}

// IsMember reports whether userID is currently in chatID (getChatMember).
// "restricted" counts only while the user is still in the chat; "left" and
// "kicked" never do.
func IsMember(ctx context.Context, client *http.Client, token string, chatID, userID int64) (bool, error) {
	raw, err := call(ctx, client, token, "getChatMember", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	})
	if err != nil {
		return false, err
	}
	var out struct {
		Result struct {
			Status   string `json:"status"`
			IsMember bool   `json:"is_member"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("telegram: decode getChatMember: %w", err)
	}
	switch out.Result.Status {
	case "creator", "administrator", "member":
		return true, nil
	case "restricted":
		return out.Result.IsMember, nil
	default:
		return false, nil
	}
}

// SetMemberTag sets the tag shown next to a regular member's name
// (setChatMemberTag, Bot API 9.5). Needs the can_manage_tags admin right; the
// tag is 0-16 characters without emoji.
func SetMemberTag(ctx context.Context, client *http.Client, token string, chatID, userID int64, tag string) error {
	_, err := call(ctx, client, token, "setChatMemberTag", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
		"tag":     tag,
	})
	return err
}

// ApproveJoinRequest lets a pending join request in. Needs can_invite_users.
func ApproveJoinRequest(ctx context.Context, client *http.Client, token string, chatID, userID int64) error {
	_, err := call(ctx, client, token, "approveChatJoinRequest", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	})
	return err
}

// CreateJoinRequestLink makes an invite link that files a join request instead
// of letting the holder straight in, so whoever ends up with it still has to
// be approved. name (0-32 characters) labels it in the chat's link list.
// Needs can_invite_users.
func CreateJoinRequestLink(ctx context.Context, client *http.Client, token string, chatID int64, name string, expire time.Time) (string, error) {
	raw, err := call(ctx, client, token, "createChatInviteLink", map[string]any{
		"chat_id":              chatID,
		"name":                 name,
		"expire_date":          expire.Unix(),
		"creates_join_request": true,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			InviteLink string `json:"invite_link"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("telegram: decode createChatInviteLink: %w", err)
	}
	if out.Result.InviteLink == "" {
		return "", fmt.Errorf("telegram: createChatInviteLink returned no link")
	}
	return out.Result.InviteLink, nil
}

// WebhookInfo is the part of getWebhookInfo that cmd/setwebhook shows.
type WebhookInfo struct {
	URL                string   `json:"url"`
	AllowedUpdates     []string `json:"allowed_updates"`
	PendingUpdateCount int      `json:"pending_update_count"`
	LastErrorMessage   string   `json:"last_error_message"`
}

// GetWebhookInfo returns the bot's current webhook registration.
func GetWebhookInfo(ctx context.Context, client *http.Client, token string) (WebhookInfo, error) {
	raw, err := call(ctx, client, token, "getWebhookInfo", map[string]any{})
	if err != nil {
		return WebhookInfo{}, err
	}
	var out struct {
		Result WebhookInfo `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return WebhookInfo{}, fmt.Errorf("telegram: decode getWebhookInfo: %w", err)
	}
	return out.Result, nil
}

// SetWebhook registers url as the bot's webhook, with the secret Telegram must
// send back in X-Telegram-Bot-Api-Secret-Token and the update kinds to send.
func SetWebhook(ctx context.Context, client *http.Client, token, url, secret string, allowed []string) error {
	_, err := call(ctx, client, token, "setWebhook", map[string]any{
		"url":             url,
		"secret_token":    secret,
		"allowed_updates": allowed,
	})
	return err
}
