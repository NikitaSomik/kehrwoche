package telegram

import (
	"context"
	"net/http"
	"time"
)

// Client binds an HTTP client and the bot token to the Bot API functions, so
// code that makes several calls — the onboarding — can take one value, and an
// interface over it, instead of threading both through every call.
type Client struct {
	HTTP  *http.Client
	Token string
}

func (c Client) SendPlain(ctx context.Context, chatID int64, text string) error {
	return SendPlain(ctx, c.HTTP, c.Token, chatID, text)
}

func (c Client) SendKeyboard(ctx context.Context, chatID int64, text string, rows [][]Button) error {
	return SendKeyboard(ctx, c.HTTP, c.Token, chatID, text, rows)
}

func (c Client) EditText(ctx context.Context, chatID int64, messageID int, text string) error {
	return EditText(ctx, c.HTTP, c.Token, chatID, messageID, text)
}

func (c Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	return AnswerCallback(ctx, c.HTTP, c.Token, callbackID, text)
}

func (c Client) IsMember(ctx context.Context, chatID, userID int64) (bool, error) {
	return IsMember(ctx, c.HTTP, c.Token, chatID, userID)
}

func (c Client) SetMemberTag(ctx context.Context, chatID, userID int64, tag string) error {
	return SetMemberTag(ctx, c.HTTP, c.Token, chatID, userID, tag)
}

func (c Client) ApproveJoinRequest(ctx context.Context, chatID, userID int64) error {
	return ApproveJoinRequest(ctx, c.HTTP, c.Token, chatID, userID)
}

func (c Client) CreateJoinRequestLink(ctx context.Context, chatID int64, name string, expire time.Time) (string, error) {
	return CreateJoinRequestLink(ctx, c.HTTP, c.Token, chatID, name, expire)
}
