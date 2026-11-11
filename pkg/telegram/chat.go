package telegram

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Button is an inline keyboard button; set exactly one of Data, URL and
// Disabled. Data (max 64 bytes) comes back in CallbackQuery.Data when it is
// pressed; URL is opened instead; a Disabled button does nothing (Bot API
// 10.3). Style colours it (Bot API 9.4); empty leaves the app's default.
type Button struct {
	Text     string          `json:"text"`
	Data     string          `json:"callback_data,omitempty"`
	URL      string          `json:"url,omitempty"`
	Disabled *DisabledButton `json:"disabled,omitempty"`
	Style    ButtonStyle     `json:"style,omitempty"`
}

// DisabledButton marks a Button that does nothing. It holds no information.
type DisabledButton struct{}

// ButtonStyle is an InlineKeyboardButton's colour.
type ButtonStyle string

const (
	StyleSuccess ButtonStyle = "success" // green
	StyleDanger  ButtonStyle = "danger"  // red
	StylePrimary ButtonStyle = "primary" // blue
)

// ProfileURL opens a user's profile by id, with no username needed. Telegram
// refuses a message carrying it when the user's privacy settings forbid it.
func ProfileURL(userID int64) string {
	return "tg://user?id=" + strconv.FormatInt(userID, 10)
}

// SendKeyboard posts a plain-text message with an inline keyboard under it.
func (c *Client) SendKeyboard(ctx context.Context, chatID int64, text string, rows [][]Button) error {
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": rows},
	}, nil)
}

// EditText replaces a bot message's text and drops its inline keyboard.
func (c *Client) EditText(ctx context.Context, chatID int64, messageID int, text string) error {
	return c.call(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}, nil)
}

// EditKeyboard replaces a bot message's inline keyboard and keeps its text.
func (c *Client) EditKeyboard(ctx context.Context, chatID int64, messageID int, rows [][]Button) error {
	return c.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"reply_markup": map[string]any{"inline_keyboard": rows},
	}, nil)
}

// AnswerCallback acknowledges a button press; a non-empty text is shown to
// the presser.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	payload := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", payload, nil)
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
func (c *Client) GetMemberStatus(ctx context.Context, chatID, userID int64) (MemberStatus, error) {
	var m ChatMember
	if err := c.call(ctx, "getChatMember", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	}, &m); err != nil {
		return "", err
	}
	return m.Role(), nil
}

// SetMemberTag sets a regular member's tag (Bot API 9.5): 0-16 characters,
// no emoji. Needs can_manage_tags.
func (c *Client) SetMemberTag(ctx context.Context, chatID, userID int64, tag string) error {
	return c.call(ctx, "setChatMemberTag", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
		"tag":     tag,
	}, nil)
}

// ApproveJoinRequest approves a join request. Needs can_invite_users.
func (c *Client) ApproveJoinRequest(ctx context.Context, chatID, userID int64) error {
	return c.call(ctx, "approveChatJoinRequest", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	}, nil)
}

// CreateJoinRequestLink makes an invite link whose users must be approved.
// name (0-32 characters) labels it in the chat's link list. Needs
// can_invite_users.
func (c *Client) CreateJoinRequestLink(ctx context.Context, chatID int64, name string, expire time.Time) (string, error) {
	var link struct {
		InviteLink string `json:"invite_link"`
	}
	if err := c.call(ctx, "createChatInviteLink", map[string]any{
		"chat_id":              chatID,
		"name":                 name,
		"expire_date":          expire.Unix(),
		"creates_join_request": true,
	}, &link); err != nil {
		return "", err
	}
	if link.InviteLink == "" {
		return "", errors.New("telegram: createChatInviteLink returned no link")
	}
	return link.InviteLink, nil
}

// RevokeInviteLink revokes a link the bot created.
func (c *Client) RevokeInviteLink(ctx context.Context, chatID int64, link string) error {
	return c.call(ctx, "revokeChatInviteLink", map[string]any{
		"chat_id":     chatID,
		"invite_link": link,
	}, nil)
}

// WebhookInfo is the part of getWebhookInfo that cmd/setwebhook shows.
type WebhookInfo struct {
	URL                string   `json:"url"`
	AllowedUpdates     []string `json:"allowed_updates"`
	PendingUpdateCount int      `json:"pending_update_count"`
	LastErrorMessage   string   `json:"last_error_message"`
}

// GetWebhookInfo reports the current webhook registration.
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	if err := c.call(ctx, "getWebhookInfo", map[string]any{}, &info); err != nil {
		return WebhookInfo{}, err
	}
	return info, nil
}

// SetWebhook registers url with the secret Telegram sends back in
// X-Telegram-Bot-Api-Secret-Token and the update kinds to deliver.
func (c *Client) SetWebhook(ctx context.Context, url, secret string, allowed []string) error {
	return c.call(ctx, "setWebhook", map[string]any{
		"url":             url,
		"secret_token":    secret,
		"allowed_updates": allowed,
	}, nil)
}
