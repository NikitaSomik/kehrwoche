// Package onboard lets a new resident into the WG group:
//
//  1. They send /start to the bot in private. The admin (ADMIN_CHAT_ID) gets a
//     card with their name and two buttons, approve and reject.
//  2. Approved, they pick their room from buttons 1..schedule.RoomCount.
//  3. The bot sends them a join-request link to the group. Their request is
//     approved by the bot itself, which then sets their member tag to the room
//     number and greets them in the group.
//
// A rejected user is never asked about again: their /start is ignored, which
// makes the rejected status the bot's block list. Somebody already in the
// group skips the admin and goes straight to choosing a room, which only
// changes their tag.
//
// Nothing is kept between updates except the users row: what a button means
// travels in its callback data, so each webhook call stands on its own.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NikitaSomik/kehrwoche/pkg/schedule"
	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
	"github.com/NikitaSomik/kehrwoche/pkg/users"
)

// Bot is the part of the Bot API the onboarding uses; telegram.Client
// satisfies it, tests use a fake.
type Bot interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.Button) error
	EditText(ctx context.Context, chatID int64, messageID int, text string) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
	IsMember(ctx context.Context, chatID, userID int64) (bool, error)
	SetMemberTag(ctx context.Context, chatID, userID int64, tag string) error
	ApproveJoinRequest(ctx context.Context, chatID, userID int64) error
	CreateJoinRequestLink(ctx context.Context, chatID int64, name string, expire time.Time) (string, error)
}

// LinkTTL is how long a join-request link stays valid. A resident who misses
// it sends /start again and gets a fresh one.
const LinkTTL = 24 * time.Hour

// Flow is one webhook call's view of the onboarding.
type Flow struct {
	Bot     Bot
	DB      users.Querier
	GroupID int64 // CHAT_ID: the group being joined
	AdminID int64 // ADMIN_CHAT_ID: where requests are approved
	Now     func() time.Time
}

// Callback data. Telegram returns it verbatim, but anyone can send any data
// from a modified client, so every press is checked against who pressed it
// and where.
const (
	actApprove = "approve"
	actReject  = "reject"
	actRoom    = "room"
)

// German texts. Sent with SendPlain: names and links would trip Markdown.
const (
	msgAskRoom     = "In welchem Zimmer wohnst du? Tippe auf deine Zimmernummer."
	msgRequestSent = "Danke! Deine Anfrage ist unterwegs. Sobald sie bestätigt ist, melde ich mich hier."
	msgPending     = "Deine Anfrage wartet noch auf Bestätigung. Ich melde mich, sobald es so weit ist."
	msgApproved    = "Deine Anfrage wurde angenommen! " + msgAskRoom
	msgRejected    = "Deine Anfrage wurde leider abgelehnt."
	msgTryLater    = "Da ist etwas schiefgelaufen. Bitte versuch es später noch einmal mit /start."
	msgNotAllowed  = "Das geht leider nicht."
	msgDone        = "Schon erledigt."
)

// Start answers /start in a private chat.
func (f Flow) Start(ctx context.Context, from *telegram.User) error {
	if from == nil {
		return nil
	}
	uid := from.ID // a private chat's id is the user's id

	member, err := f.Bot.IsMember(ctx, f.GroupID, uid)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("check membership: %w", err))
	}
	if member {
		return f.Bot.SendKeyboard(ctx, uid, "Hallo! "+msgAskRoom, roomKeyboard())
	}

	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("read user: %w", err))
	}
	switch u.Status {
	case users.StatusRejected:
		return nil // the block list: no answer, no new request
	case users.StatusPending:
		return f.Bot.SendPlain(ctx, uid, msgPending)
	case users.StatusApproved:
		if u.Room == 0 {
			return f.Bot.SendKeyboard(ctx, uid, msgAskRoom, roomKeyboard())
		}
		return f.sendLink(ctx, uid)
	}

	created, err := users.RequestJoin(ctx, f.DB, uid)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("request join: %w", err))
	}
	if !created {
		// A second /start overtook this one; that call tells the admin.
		return f.Bot.SendPlain(ctx, uid, msgPending)
	}
	if err := f.Bot.SendKeyboard(ctx, f.AdminID, requestCard(from), decisionKeyboard(uid)); err != nil {
		// Nobody would ever answer a request the admin never saw, so take it
		// back and let the user try again.
		if cerr := users.CancelRequest(ctx, f.DB, uid); cerr != nil {
			err = errors.Join(err, fmt.Errorf("cancel request: %w", cerr))
		}
		return f.failed(ctx, uid, fmt.Errorf("notify admin: %w", err))
	}
	return f.Bot.SendPlain(ctx, uid, msgRequestSent)
}

// Callback handles a press of one of the onboarding's buttons. The press is
// always acknowledged, so the client's spinner stops whatever happens.
func (f Flow) Callback(ctx context.Context, q *telegram.CallbackQuery) (err error) {
	notice := ""
	defer func() {
		if aerr := f.Bot.AnswerCallback(ctx, q.ID, notice); aerr != nil {
			err = errors.Join(err, fmt.Errorf("answer callback: %w", aerr))
		}
	}()

	if q.From == nil || q.Message == nil || q.Message.Chat == nil {
		return nil
	}
	act, arg, ok := parseData(q.Data)
	if !ok {
		notice = msgNotAllowed
		return nil
	}
	switch act {
	case actApprove, actReject:
		// Only from the admin chat: the card is sent nowhere else, and a
		// forged press from any other chat must not let anybody in.
		if q.Message.Chat.ID != f.AdminID {
			notice = msgNotAllowed
			return nil
		}
		notice, err = f.decide(ctx, q, arg, act == actApprove)
		return err
	case actRoom:
		// Only in the presser's own private chat, and only a real room.
		if q.Message.Chat.ID != q.From.ID || !schedule.ValidRoom(int(arg)) {
			notice = msgNotAllowed
			return nil
		}
		notice, err = f.chooseRoom(ctx, q, int(arg))
		return err
	}
	return nil
}

func (f Flow) decide(ctx context.Context, q *telegram.CallbackQuery, uid int64, approve bool) (string, error) {
	changed, err := users.Decide(ctx, f.DB, uid, approve)
	if err != nil {
		return msgTryLater, fmt.Errorf("decide: %w", err)
	}
	if !changed {
		return msgDone, nil
	}

	verdict := "❌ Abgelehnt"
	if approve {
		verdict = "✅ Angenommen"
	}
	var errs []error
	if err := f.Bot.EditText(ctx, q.Message.Chat.ID, q.Message.MessageID, q.Message.Text+"\n\n"+verdict); err != nil {
		errs = append(errs, fmt.Errorf("retire card: %w", err))
	}
	if approve {
		err = f.Bot.SendKeyboard(ctx, uid, msgApproved, roomKeyboard())
	} else {
		err = f.Bot.SendPlain(ctx, uid, msgRejected)
	}
	if err != nil {
		// The decision stands either way; an approved user who never got the
		// buttons gets them with their next /start.
		errs = append(errs, fmt.Errorf("tell user: %w", err))
	}
	return verdict, errors.Join(errs...)
}

func (f Flow) chooseRoom(ctx context.Context, q *telegram.CallbackQuery, room int) (string, error) {
	uid := q.From.ID
	member, err := f.Bot.IsMember(ctx, f.GroupID, uid)
	if err != nil {
		return msgTryLater, fmt.Errorf("check membership: %w", err)
	}
	if member {
		if err := f.Bot.SetMemberTag(ctx, f.GroupID, uid, Tag(room)); err != nil {
			return msgTryLater, fmt.Errorf("set tag: %w", err)
		}
		if err := users.SetMemberRoom(ctx, f.DB, uid, room); err != nil {
			return "", fmt.Errorf("store room: %w", err) // the tag is set; that's what counts
		}
		return "", f.Bot.EditText(ctx, q.Message.Chat.ID, q.Message.MessageID, fmt.Sprintf("Zimmer %d ist gespeichert ✅", room))
	}

	changed, err := users.SetApprovedRoom(ctx, f.DB, uid, room)
	if err != nil {
		return msgTryLater, fmt.Errorf("store room: %w", err)
	}
	if !changed {
		return msgNotAllowed, nil // not approved: the buttons can't skip the admin
	}
	if err := f.Bot.EditText(ctx, q.Message.Chat.ID, q.Message.MessageID, fmt.Sprintf("Zimmer %d ✅", room)); err != nil {
		return "", errors.Join(fmt.Errorf("retire buttons: %w", err), f.sendLink(ctx, uid))
	}
	return "", f.sendLink(ctx, uid)
}

// JoinRequest handles a request to join the group. Only an approved user with
// a room is let in; anybody else's request is left for a human admin.
func (f Flow) JoinRequest(ctx context.Context, r *telegram.ChatJoinRequest) error {
	if r.Chat == nil || r.Chat.ID != f.GroupID || r.From == nil {
		return nil
	}
	uid := r.From.ID
	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return fmt.Errorf("read user: %w", err)
	}
	if u.Status != users.StatusApproved || u.Room == 0 {
		return nil
	}
	if err := f.Bot.ApproveJoinRequest(ctx, f.GroupID, uid); err != nil {
		return fmt.Errorf("approve: %w", err)
	}

	var errs []error
	if err := f.Bot.SetMemberTag(ctx, f.GroupID, uid, Tag(u.Room)); err != nil {
		errs = append(errs, fmt.Errorf("set tag: %w", err))
	}
	if err := f.Bot.SendPlain(ctx, f.GroupID, welcome(r.From, u.Room)); err != nil {
		errs = append(errs, fmt.Errorf("welcome: %w", err))
	}
	return errors.Join(errs...)
}

func (f Flow) sendLink(ctx context.Context, uid int64) error {
	link, err := f.Bot.CreateJoinRequestLink(ctx, f.GroupID, fmt.Sprintf("Onboarding %d", uid), f.Now().Add(LinkTTL))
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("create link: %w", err))
	}
	return f.Bot.SendPlain(ctx, uid,
		"Hier ist dein Link zur WG-Gruppe, 24 Stunden gültig:\n"+link+
			"\n\nÖffne ihn und tippe auf „Beitritt anfragen“ – ich lasse dich dann sofort rein.")
}

// failed tells the user something went wrong and returns err for the log.
func (f Flow) failed(ctx context.Context, uid int64, err error) error {
	if serr := f.Bot.SendPlain(ctx, uid, msgTryLater); serr != nil {
		err = errors.Join(err, fmt.Errorf("tell user: %w", serr))
	}
	return err
}

// Tag is the member tag for a room: the bare number, as the residents already
// tagged by hand have it.
func Tag(room int) string {
	return strconv.Itoa(room)
}

func requestCard(u *telegram.User) string {
	var b strings.Builder
	b.WriteString("Neue Anfrage für die WG-Gruppe:\n")
	b.WriteString(u.Name())
	if u.Username != "" {
		b.WriteString(" (@" + u.Username + ")")
	}
	fmt.Fprintf(&b, "\nID: %d", u.ID)
	return b.String()
}

func welcome(u *telegram.User, room int) string {
	greeting := "👋 Willkommen in der WG"
	if name := u.Name(); name != "" {
		greeting += ", " + name
	}
	return fmt.Sprintf("%s! Zimmer %d.", greeting, room)
}

func decisionKeyboard(uid int64) [][]telegram.Button {
	id := strconv.FormatInt(uid, 10)
	return [][]telegram.Button{{
		{Text: "✅ Annehmen", Data: actApprove + ":" + id},
		{Text: "❌ Ablehnen", Data: actReject + ":" + id},
	}}
}

// roomKeyboard lays the rooms out four to a row.
func roomKeyboard() [][]telegram.Button {
	var rows [][]telegram.Button
	for n := 1; n <= schedule.RoomCount; n++ {
		if (n-1)%4 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], telegram.Button{Text: strconv.Itoa(n), Data: actRoom + ":" + strconv.Itoa(n)})
	}
	return rows
}

// parseData splits callback data into an action and its positive integer
// argument. Anything else — an unknown action, a sign, spaces, a number that
// doesn't fit — is rejected.
func parseData(data string) (act string, arg int64, ok bool) {
	act, rest, found := strings.Cut(data, ":")
	if !found || rest == "" || rest[0] < '1' || rest[0] > '9' {
		return "", 0, false
	}
	switch act {
	case actApprove, actReject, actRoom:
	default:
		return "", 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return act, n, true
}
