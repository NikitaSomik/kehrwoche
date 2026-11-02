// Package onboard lets a new resident into the WG group:
//
//  1. /start in private sends the admin an approve/reject card.
//  2. Once approved, the resident picks a room (1..schedule.RoomCount).
//  3. The bot sends a join-request link, approves the resulting request, sets
//     the member tag to the room number and greets them in the group.
//
// The request is approved only through the link issued to that user, which is
// revoked once used. Rejected users are ignored from then on; members just get
// a greeting. The only state between updates is the users row.
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
// satisfies it.
type Bot interface {
	SendPlain(ctx context.Context, chatID int64, text string) error
	SendKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.Button) error
	EditText(ctx context.Context, chatID int64, messageID int, text string) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
	SetMemberTag(ctx context.Context, chatID, userID int64, tag string) error
	ApproveJoinRequest(ctx context.Context, chatID, userID int64) error
	RevokeInviteLink(ctx context.Context, chatID int64, link string) error
	CreateJoinRequestLink(ctx context.Context, chatID int64, name string, expire time.Time) (string, error)
	GetMemberStatus(ctx context.Context, chatID, userID int64) (telegram.MemberStatus, error)
}

// LinkTTL is how long a join-request link stays valid. /start sends the
// current link again while more than linkMinLeft of it remains, and a new one
// after that.
const (
	LinkTTL     = 24 * time.Hour
	linkMinLeft = time.Hour
)

// Flow handles the onboarding updates of one webhook call.
type Flow struct {
	Bot     Bot
	DB      users.Querier
	GroupID int64 // CHAT_ID
	AdminID int64 // ADMIN_CHAT_ID: where the approval cards go
	Now     func() time.Time
}

// Callback data actions: "approve:<user id>", "reject:<user id>", "room:<n>",
// "change:<user id>" (pick the room again before joining).
const (
	actApprove = "approve"
	actReject  = "reject"
	actRoom    = "room"
	actChange  = "change"
)

// Sent as plain text: names and links would trip Markdown.
const (
	msgMember      = "Hallo! Du bist schon in der WG-Gruppe."
	msgAskRoom     = "In welchem Zimmer wohnst du? Tippe auf deine Zimmernummer."
	msgRequestSent = "Danke! Deine Anfrage ist unterwegs. Sobald sie bestätigt ist, melde ich mich hier."
	msgPending     = "Deine Anfrage wartet noch auf Bestätigung. Ich melde mich, sobald es so weit ist."
	msgApproved    = "Deine Anfrage wurde angenommen! " + msgAskRoom
	msgRejected    = "Deine Anfrage wurde leider abgelehnt."
	msgTryLater    = "Da ist etwas schiefgelaufen. Bitte versuch es später noch einmal mit /start."
	msgNotAllowed  = "Das geht leider nicht."
	msgDone        = "Schon erledigt."
)

// Start answers /start in a private chat from a user who is not a group
// admin; status is their role in the group.
func (f Flow) Start(ctx context.Context, from *telegram.User, status telegram.MemberStatus) error {
	if from == nil || status == telegram.StatusKicked {
		return nil
	}
	uid := from.ID // equals the private chat's id
	if status.InChat() {
		return f.Bot.SendPlain(ctx, uid, msgMember)
	}

	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("read user: %w", err))
	}
	switch u.Status {
	case users.StatusRejected:
		return nil
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
	if !created { // a concurrent /start filed it
		return f.Bot.SendPlain(ctx, uid, msgPending)
	}
	card := requestCard(from)
	err = f.Bot.SendKeyboard(ctx, f.AdminID, card, decisionKeyboard(uid, true))
	if err != nil {
		// Most likely the user's privacy settings refuse the profile button.
		err = f.Bot.SendKeyboard(ctx, f.AdminID, card, decisionKeyboard(uid, false))
	}
	if err != nil {
		// Withdraw it so the user can retry.
		if cerr := users.CancelRequest(ctx, f.DB, uid); cerr != nil {
			err = errors.Join(err, fmt.Errorf("cancel request: %w", cerr))
		}
		return f.failed(ctx, uid, fmt.Errorf("notify admin: %w", err))
	}
	return f.Bot.SendPlain(ctx, uid, msgRequestSent)
}

// Callback handles a button press. Every press is answered, so the client's
// spinner always stops. Callback data can be forged, so each press is checked
// against who pressed it and where.
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
		// Only an admin of the WG group decides. The role is the whole check:
		// the chat the card sits in may hold others, and a forged press from
		// a group admin decides nothing they couldn't decide anyway.
		status, err := f.Bot.GetMemberStatus(ctx, f.GroupID, q.From.ID)
		if err != nil {
			notice = msgTryLater
			return fmt.Errorf("presser's role: %w", err)
		}
		if !status.IsAdmin() {
			notice = msgNotAllowed
			return nil
		}
		notice, err = f.decide(ctx, q, arg, act == actApprove)
		return err
	case actRoom:
		if q.Message.Chat.ID != q.From.ID || !schedule.ValidRoom(int(arg)) {
			notice = msgNotAllowed
			return nil
		}
		notice, err = f.chooseRoom(ctx, q, int(arg))
		return err
	case actChange:
		if q.Message.Chat.ID != q.From.ID || arg != q.From.ID {
			notice = msgNotAllowed
			return nil
		}
		notice, err = f.changeRoom(ctx, q.From.ID)
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
	card := q.Message.Text + "\n\n" + verdict
	if name := q.From.Name(); name != "" {
		card += " von " + name
	}
	if err := f.Bot.EditText(ctx, q.Message.Chat.ID, q.Message.MessageID, card); err != nil {
		errs = append(errs, fmt.Errorf("retire card: %w", err))
	}
	if approve {
		err = f.Bot.SendKeyboard(ctx, uid, msgApproved, roomKeyboard())
	} else {
		err = f.Bot.SendPlain(ctx, uid, msgRejected)
	}
	if err != nil { // the decision stands; /start resends the buttons
		errs = append(errs, fmt.Errorf("tell user: %w", err))
	}
	return verdict, errors.Join(errs...)
}

func (f Flow) chooseRoom(ctx context.Context, q *telegram.CallbackQuery, room int) (string, error) {
	uid := q.From.ID
	changed, err := users.SetApprovedRoom(ctx, f.DB, uid, room)
	if err != nil {
		return msgTryLater, fmt.Errorf("store room: %w", err)
	}
	if !changed {
		return msgNotAllowed, nil // not approved
	}
	if err := f.Bot.EditText(ctx, q.Message.Chat.ID, q.Message.MessageID, fmt.Sprintf("Zimmer %d ✅", room)); err != nil {
		return "", errors.Join(fmt.Errorf("retire buttons: %w", err), f.sendLink(ctx, uid))
	}
	return "", f.sendLink(ctx, uid)
}

// changeRoom shows the room buttons again to an approved user who has not
// joined yet. Their link stays valid: the room is read when they join.
func (f Flow) changeRoom(ctx context.Context, uid int64) (string, error) {
	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return msgTryLater, fmt.Errorf("read user: %w", err)
	}
	if u.Status != users.StatusApproved {
		return msgNotAllowed, nil // joined, or never approved
	}
	return "", f.Bot.SendKeyboard(ctx, uid, msgAskRoom, roomKeyboard())
}

// JoinRequest approves a join request from an approved user with a room and
// leaves every other request to a human admin.
func (f Flow) JoinRequest(ctx context.Context, r *telegram.ChatJoinRequest) error {
	if r.Chat == nil || r.Chat.ID != f.GroupID || r.From == nil {
		return nil
	}
	uid := r.From.ID
	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return fmt.Errorf("read user: %w", err)
	}
	// Telegram sets From, so it can't be forged; the link must be the one
	// issued to this user, so a forwarded link gets nobody else in.
	if u.Status != users.StatusApproved || u.Room == 0 || u.InviteLink == "" ||
		r.InviteLink == nil || r.InviteLink.InviteLink != u.InviteLink {
		return nil
	}
	if err := f.Bot.ApproveJoinRequest(ctx, f.GroupID, uid); err != nil {
		return fmt.Errorf("approve: %w", err)
	}

	var errs []error
	if _, err := users.MarkJoined(ctx, f.DB, uid); err != nil {
		errs = append(errs, fmt.Errorf("mark joined: %w", err))
	}
	if err := f.Bot.RevokeInviteLink(ctx, f.GroupID, u.InviteLink); err != nil {
		errs = append(errs, fmt.Errorf("revoke link: %w", err))
	}
	if err := f.Bot.SetMemberTag(ctx, f.GroupID, uid, Tag(u.Room)); err != nil {
		errs = append(errs, fmt.Errorf("set tag: %w", err))
	}
	if err := f.Bot.SendPlain(ctx, f.GroupID, welcome(r.From, u.Room)); err != nil {
		errs = append(errs, fmt.Errorf("welcome: %w", err))
	}
	return errors.Join(errs...)
}

// sendLink issues a fresh join-request link, stores it as the only one the
// user may join through, and revokes the previous one.
func (f Flow) sendLink(ctx context.Context, uid int64) error {
	u, _, err := users.Get(ctx, f.DB, uid)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("read user: %w", err))
	}
	if u.InviteLink != "" && u.InviteExpires.After(f.Now().Add(linkMinLeft)) {
		return f.Bot.SendKeyboard(ctx, uid, linkMessage(u.InviteLink, u.InviteExpires, u.Room), changeKeyboard(uid))
	}
	expires := f.Now().Add(LinkTTL).Truncate(time.Second) // what Telegram keeps
	link, err := f.Bot.CreateJoinRequestLink(ctx, f.GroupID, fmt.Sprintf("Onboarding %d", uid), expires)
	if err != nil {
		return f.failed(ctx, uid, fmt.Errorf("create link: %w", err))
	}
	stored, err := users.SetInviteLink(ctx, f.DB, uid, link, expires)
	if err == nil && !stored {
		err = errors.New("user is no longer approved")
	}
	if err != nil {
		rerr := f.Bot.RevokeInviteLink(ctx, f.GroupID, link)
		return f.failed(ctx, uid, errors.Join(fmt.Errorf("store link: %w", err), rerr))
	}
	var errs []error
	if u.InviteLink != "" {
		if err := f.Bot.RevokeInviteLink(ctx, f.GroupID, u.InviteLink); err != nil {
			errs = append(errs, fmt.Errorf("revoke previous link: %w", err))
		}
	}
	err = f.Bot.SendKeyboard(ctx, uid, linkMessage(link, expires, u.Room), changeKeyboard(uid))
	return errors.Join(append(errs, err)...)
}

// linkMessage hands the user their link, with its expiry in Berlin time and
// the room they picked.
func linkMessage(link string, expires time.Time, room int) string {
	if loc, err := time.LoadLocation("Europe/Berlin"); err == nil {
		expires = expires.In(loc)
	}
	return fmt.Sprintf("Dein Zimmer: %d.\n\n", room) +
		"Hier ist dein Link zur WG-Gruppe, gültig bis " + expires.Format("02.01. 15:04") + " Uhr:\n" + link +
		"\n\nÖffne ihn und tippe auf „Beitritt anfragen“ – ich lasse dich dann sofort rein."
}

// changeKeyboard sits under the link: a wrong room can be fixed until the
// user joins.
func changeKeyboard(uid int64) [][]telegram.Button {
	return [][]telegram.Button{{{Text: "Zimmer ändern", Data: actChange + ":" + strconv.FormatInt(uid, 10)}}}
}

// failed tells the user something went wrong and returns err for the log.
func (f Flow) failed(ctx context.Context, uid int64, err error) error {
	if serr := f.Bot.SendPlain(ctx, uid, msgTryLater); serr != nil {
		err = errors.Join(err, fmt.Errorf("tell user: %w", serr))
	}
	return err
}

// Tag is the member tag for a room: the bare number.
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

// decisionKeyboard is the card's ✅ / ❌ row, under a button opening the
// user's real profile when withProfile: names and usernames are anyone's to
// pick, the profile behind the id is not.
func decisionKeyboard(uid int64, withProfile bool) [][]telegram.Button {
	id := strconv.FormatInt(uid, 10)
	var rows [][]telegram.Button
	if withProfile {
		rows = append(rows, []telegram.Button{{Text: "👤 Profil öffnen", URL: telegram.ProfileURL(uid)}})
	}
	return append(rows, []telegram.Button{
		{Text: "✅ Annehmen", Data: actApprove + ":" + id},
		{Text: "❌ Ablehnen", Data: actReject + ":" + id},
	})
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

// parseData splits callback data into a known action and a positive integer
// without sign or leading zero; anything else is rejected.
func parseData(data string) (act string, arg int64, ok bool) {
	act, rest, found := strings.Cut(data, ":")
	if !found || rest == "" || rest[0] < '1' || rest[0] > '9' {
		return "", 0, false
	}
	switch act {
	case actApprove, actReject, actRoom, actChange:
	default:
		return "", 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return act, n, true
}
