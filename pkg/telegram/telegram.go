// Package telegram is the slice of the Telegram Bot API the bot uses: Client
// for outbound calls, Update and friends for the webhook payload.
package telegram

import "context"

// Command is one entry in the bot's command menu (Telegram's setMyCommands).
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Send posts a Markdown-formatted message to the given Telegram chat.
func (c *Client) Send(ctx context.Context, chatID int64, text string) error {
	return c.sendMessage(ctx, chatID, text, "Markdown")
}

// SendPlain posts a message with no parse mode, for text that would trip
// Markdown parsing (e.g. command names containing underscores).
func (c *Client) SendPlain(ctx context.Context, chatID int64, text string) error {
	return c.sendMessage(ctx, chatID, text, "")
}

func (c *Client) sendMessage(ctx context.Context, chatID int64, text, parseMode string) error {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	return c.call(ctx, "sendMessage", payload, nil)
}

// SetCommands replaces the bot's command menu (setMyCommands) for the given
// scope; an empty scope means the default scope. Scope is a BotCommandScope
// type string, e.g. "all_private_chats", "all_group_chats".
func (c *Client) SetCommands(ctx context.Context, cmds []Command, scope string) error {
	payload := map[string]any{"commands": cmds}
	addScope(payload, scope)
	return c.call(ctx, "setMyCommands", payload, nil)
}

// SetChatCommands sets the command menu of one chat (BotCommandScopeChat),
// which takes precedence over every broader scope there.
func (c *Client) SetChatCommands(ctx context.Context, cmds []Command, chatID int64) error {
	return c.call(ctx, "setMyCommands", map[string]any{
		"commands": cmds,
		"scope":    map[string]any{"type": "chat", "chat_id": chatID},
	}, nil)
}

// DeleteCommands removes the command menu of a scope (deleteMyCommands); an
// empty scope means the default scope.
func (c *Client) DeleteCommands(ctx context.Context, scope string) error {
	payload := map[string]any{}
	addScope(payload, scope)
	return c.call(ctx, "deleteMyCommands", payload, nil)
}

// GetCommands returns the bot's command menu (getMyCommands) for the given
// scope; an empty scope means the default scope.
func (c *Client) GetCommands(ctx context.Context, scope string) ([]Command, error) {
	payload := map[string]any{}
	addScope(payload, scope)
	var cmds []Command
	if err := c.call(ctx, "getMyCommands", payload, &cmds); err != nil {
		return nil, err
	}
	return cmds, nil
}

// addScope sets a broad BotCommandScope; an empty one is left out, which
// Telegram reads as the default scope.
func addScope(payload map[string]any, scope string) {
	if scope != "" {
		payload["scope"] = map[string]string{"type": scope}
	}
}
