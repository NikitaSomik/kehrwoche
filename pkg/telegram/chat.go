package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Button is an inline keyboard button; set exactly one of Data and URL. Data
// (max 64 bytes) comes back in CallbackQuery.Data when it is pressed; URL is
// opened instead.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// ProfileURL opens a user's profile by id, with no username needed. Telegram
// refuses a message carrying it when the user's privacy settings forbid it.
func ProfileURL(userID int64) string {
	return "tg://user?id=" + strconv.FormatInt(userID, 10)
}

func SendKeyboard(ctx context.Context, client *http.Client, token string, chatID int64, text string, rows [][]Button) error {
	_, err := call(ctx, client, token, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": rows},
	})
	return err
}

// EditText replaces a bot message's text and drops its inline keyboard.
func EditText(ctx context.Context, client *http.Client, token string, chatID int64, messageID int, text string) error {
	_, err := call(ctx, client, token, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	})
	return err
}

// AnswerCallback acknowledges a button press; a non-empty text is shown to
// the presser.
func AnswerCallback(ctx context.Context, client *http.Client, token, callbackID, text string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	_, err := call(ctx, client, token, "answerCallbackQuery", payload)
	return err
}

// MemberStatus is a user's role in a chat, as getChatMember reports it.
type MemberStatus string

const (
	StatusCreator       MemberStatus = "creator"
	StatusAdministrator MemberStatus = "administrator"
	StatusMember        MemberStatus = "member"
	StatusLeft          MemberStatus = "left" // not in the chat: left, or never joined
	StatusKicked        MemberStatus = "kicked"
)

func (s MemberStatus) IsAdmin() bool {
	return s == StatusCreator || s == StatusAdministrator
}

func (s MemberStatus) InChat() bool {
	return s.IsAdmin() || s == StatusMember
}

// GetMemberStatus returns userID's role in chatID. A restricted user counts
// as a member while still in the chat and as left otherwise.
func GetMemberStatus(ctx context.Context, client *http.Client, token string, chatID, userID int64) (MemberStatus, error) {
	raw, err := call(ctx, client, token, "getChatMember", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Result ChatMember `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("telegram: decode getChatMember: %w", err)
	}
	return out.Result.Role(), nil
}

// SetMemberTag sets a regular member's tag (Bot API 9.5): 0-16 characters,
// no emoji. Needs can_manage_tags.
func SetMemberTag(ctx context.Context, client *http.Client, token string, chatID, userID int64, tag string) error {
	_, err := call(ctx, client, token, "setChatMemberTag", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
		"tag":     tag,
	})
	return err
}

// ApproveJoinRequest approves a join request. Needs can_invite_users.
func ApproveJoinRequest(ctx context.Context, client *http.Client, token string, chatID, userID int64) error {
	_, err := call(ctx, client, token, "approveChatJoinRequest", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	})
	return err
}

// CreateJoinRequestLink makes an invite link whose users must be approved.
// name (0-32 characters) labels it in the chat's link list. Needs
// can_invite_users.
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

// RevokeInviteLink revokes a link the bot created.
func RevokeInviteLink(ctx context.Context, client *http.Client, token string, chatID int64, link string) error {
	_, err := call(ctx, client, token, "revokeChatInviteLink", map[string]any{
		"chat_id":     chatID,
		"invite_link": link,
	})
	return err
}

// WebhookInfo is the part of getWebhookInfo that cmd/setwebhook shows.
type WebhookInfo struct {
	URL                string   `json:"url"`
	AllowedUpdates     []string `json:"allowed_updates"`
	PendingUpdateCount int      `json:"pending_update_count"`
	LastErrorMessage   string   `json:"last_error_message"`
}

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

// SetWebhook registers url with the secret Telegram sends back in
// X-Telegram-Bot-Api-Secret-Token and the update kinds to deliver.
func SetWebhook(ctx context.Context, client *http.Client, token, url, secret string, allowed []string) error {
	_, err := call(ctx, client, token, "setWebhook", map[string]any{
		"url":             url,
		"secret_token":    secret,
		"allowed_updates": allowed,
	})
	return err
}
