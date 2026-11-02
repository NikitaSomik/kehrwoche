package onboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NikitaSomik/kehrwoche/pkg/telegram"
	"github.com/NikitaSomik/kehrwoche/pkg/users"
)

const (
	groupID = int64(-100500)
	adminID = int64(42)
	userID  = int64(777)
)

// fakeDB plays the users table in memory, recognising each pkg/users
// statement by a fragment of its SQL. The real SQL has integration tests.
type fakeDB struct {
	rows map[int64]users.User
	err  error
}

func newDB(rows map[int64]users.User) *fakeDB {
	if rows == nil {
		rows = map[int64]users.User{}
	}
	return &fakeDB{rows: rows}
}

func (d *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.err != nil {
		return pgconn.CommandTag{}, d.err
	}
	id := args[0].(int64)
	u, found := d.rows[id]
	affected := false
	switch {
	case strings.Contains(sql, "INSERT INTO users (tg_user_id, left_at)"):
		u.LeftAt = time.Now()
		affected = true
	case strings.Contains(sql, "INSERT INTO users (tg_user_id, status)"):
		if !found || u.Status == users.StatusNone {
			u.Status, u.Room = users.StatusPending, 0
			affected = true
		}
	case strings.Contains(sql, "SET status = NULL") && strings.Contains(sql, "status = 'approved'"):
		if u.Status == users.StatusApproved {
			u.Status, u.InviteLink, u.InviteExpires, u.JoinedAt, u.LeftAt = users.StatusNone, "", time.Time{}, time.Now(), time.Time{}
			affected = true
		}
	case strings.Contains(sql, "SET invite_link = $2"):
		if u.Status == users.StatusApproved {
			u.InviteLink, u.InviteExpires = args[1].(string), args[2].(time.Time)
			affected = true
		}
	case strings.Contains(sql, "SET status = NULL"):
		if u.Status == users.StatusPending {
			u.Status = users.StatusNone
			affected = true
		}
	case strings.Contains(sql, "SET status = $2"):
		if u.Status == users.StatusPending {
			u.Status = users.Status(args[1].(string))
			affected = true
		}
	case strings.Contains(sql, "UPDATE users SET room"):
		if u.Status == users.StatusApproved {
			u.Room = args[1].(int)
			affected = true
		}
	default:
		return pgconn.CommandTag{}, fmt.Errorf("fakeDB: unexpected statement %q", sql)
	}
	if affected {
		d.rows[id] = u
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (d *fakeDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if d.err != nil {
		return row{err: d.err}
	}
	u, found := d.rows[args[0].(int64)]
	if !found {
		return row{err: pgx.ErrNoRows}
	}
	return row{u: u}
}

type row struct {
	u   users.User
	err error
}

func (r row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = string(r.u.Status)
	*dest[1].(*int) = r.u.Room
	*dest[2].(*string) = r.u.InviteLink
	if !r.u.InviteExpires.IsZero() {
		exp := r.u.InviteExpires
		*dest[3].(**time.Time) = &exp
	}
	if !r.u.JoinedAt.IsZero() {
		joined := r.u.JoinedAt
		*dest[4].(**time.Time) = &joined
	}
	if !r.u.LeftAt.IsZero() {
		left := r.u.LeftAt
		*dest[5].(**time.Time) = &left
	}
	return nil
}

type sent struct {
	chatID int64
	text   string
	rows   [][]telegram.Button
}

type fakeBot struct {
	sendErr        error
	privateProfile bool // refuse URL buttons, as Telegram does for a hidden profile

	sent     []sent
	edits    []string
	answers  []string
	tags     map[int64]string
	approved []int64
	links    int
	revoked  []string
	// roles are users' roles in the WG group; adminID is its creator and
	// anyone missing has left.
	roles map[int64]telegram.MemberStatus
}

func (b *fakeBot) GetMemberStatus(_ context.Context, _ int64, userID int64) (telegram.MemberStatus, error) {
	if st, ok := b.roles[userID]; ok {
		return st, nil
	}
	if userID == adminID {
		return telegram.StatusCreator, nil
	}
	return telegram.StatusLeft, nil
}

func (b *fakeBot) SendPlain(_ context.Context, chatID int64, text string) error {
	if b.sendErr != nil {
		return b.sendErr
	}
	b.sent = append(b.sent, sent{chatID: chatID, text: text})
	return nil
}

func (b *fakeBot) SendKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.Button) error {
	if b.sendErr != nil {
		return b.sendErr
	}
	if b.privateProfile {
		for _, row := range rows {
			for _, btn := range row {
				if btn.URL != "" {
					return errors.New("BUTTON_USER_PRIVACY_RESTRICTED")
				}
			}
		}
	}
	b.sent = append(b.sent, sent{chatID: chatID, text: text, rows: rows})
	return nil
}

func (b *fakeBot) EditText(_ context.Context, _ int64, _ int, text string) error {
	b.edits = append(b.edits, text)
	return nil
}

func (b *fakeBot) AnswerCallback(_ context.Context, _ string, text string) error {
	b.answers = append(b.answers, text)
	return nil
}

func (b *fakeBot) SetMemberTag(_ context.Context, _ int64, userID int64, tag string) error {
	if b.tags == nil {
		b.tags = map[int64]string{}
	}
	b.tags[userID] = tag
	return nil
}

func (b *fakeBot) ApproveJoinRequest(_ context.Context, _ int64, userID int64) error {
	b.approved = append(b.approved, userID)
	return nil
}

func (b *fakeBot) CreateJoinRequestLink(context.Context, int64, string, time.Time) (string, error) {
	b.links++
	return fmt.Sprintf("https://t.me/+link%d", b.links), nil
}

func (b *fakeBot) RevokeInviteLink(_ context.Context, _ int64, link string) error {
	b.revoked = append(b.revoked, link)
	return nil
}

func (b *fakeBot) last() sent {
	if len(b.sent) == 0 {
		return sent{}
	}
	return b.sent[len(b.sent)-1]
}

func flow(bot *fakeBot, db *fakeDB) Flow {
	return Flow{Bot: bot, DB: db, GroupID: groupID, AdminID: adminID, Now: time.Now}
}

var newcomer = &telegram.User{ID: userID, FirstName: "Anna", LastName: "Muster", Username: "anna"}

func TestStart(t *testing.T) {
	ctx := context.Background()

	t.Run("a stranger's request reaches the admin", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusPending {
			t.Errorf("status %q, want pending", db.rows[userID].Status)
		}
		if len(bot.sent) != 2 {
			t.Fatalf("got %d messages, want the admin card and the user's confirmation", len(bot.sent))
		}
		card := bot.sent[0]
		if card.chatID != adminID || !strings.Contains(card.text, "Anna Muster (@anna)") || strings.Contains(card.text, "War schon") || len(card.rows) != 2 ||
			card.rows[0][0].URL != "tg://user?id=777" {
			t.Errorf("admin card: %+v", card)
		}
		if bot.sent[1].chatID != userID || bot.sent[1].text != msgRequestSent {
			t.Errorf("user reply: %+v", bot.sent[1])
		}
	})

	t.Run("a pending user is told to wait and the admin is not asked again", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 1 || bot.sent[0].chatID != userID || bot.sent[0].text != msgPending {
			t.Errorf("got %+v", bot.sent)
		}
	})

	t.Run("a rejected user gets no answer at all", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusRejected}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 0 {
			t.Errorf("got %+v, want silence", bot.sent)
		}
	})

	t.Run("an approved user without a room is asked again", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if len(bot.last().rows) == 0 {
			t.Errorf("got %+v, want the room keyboard", bot.sent)
		}
	})

	t.Run("an approved user with a room gets their link again while it lasts", func(t *testing.T) {
		bot := &fakeBot{}
		expires := time.Now().Add(2 * linkMinLeft)
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 3, InviteLink: "https://t.me/+old", InviteExpires: expires}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if bot.links != 0 || len(bot.revoked) != 0 || !strings.Contains(bot.last().text, "https://t.me/+old") {
			t.Errorf("links %d, revoked %v, sent %+v", bot.links, bot.revoked, bot.sent)
		}
	})

	t.Run("an approved user with a room gets a fresh link when theirs runs out", func(t *testing.T) {
		bot := &fakeBot{}
		expires := time.Now().Add(linkMinLeft / 2)
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 3, InviteLink: "https://t.me/+old", InviteExpires: expires}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if bot.links != 1 || !strings.Contains(bot.last().text, "https://t.me/+link1") {
			t.Errorf("got %+v", bot.sent)
		}
		if db.rows[userID].InviteLink != "https://t.me/+link1" {
			t.Errorf("stored link %q", db.rows[userID].InviteLink)
		}
		if len(bot.revoked) != 1 || bot.revoked[0] != "https://t.me/+old" {
			t.Errorf("revoked %v, want the previous link", bot.revoked)
		}
	})

	t.Run("a member just gets a greeting", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusMember); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 1 || bot.sent[0].text != msgMember || len(bot.sent[0].rows) != 0 {
			t.Errorf("got %+v, want only the greeting", bot.sent)
		}
		if _, found := db.rows[userID]; found {
			t.Error("a member's /start must not file a request")
		}
	})

	t.Run("a hidden profile sends the card without its button", func(t *testing.T) {
		bot, db := &fakeBot{privateProfile: true}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 2 || len(bot.sent[0].rows) != 1 || db.rows[userID].Status != users.StatusPending {
			t.Errorf("sent %+v, status %q", bot.sent, db.rows[userID].Status)
		}
	})

	t.Run("a former member picks a room again, and the card says they were here", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Room: 3, JoinedAt: time.Now().Add(-time.Hour)}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusPending || db.rows[userID].Room != 0 {
			t.Errorf("row %+v, want pending with no room", db.rows[userID])
		}
		if card := bot.sent[0].text; !strings.HasSuffix(card, "War schon Mitglied der WG.") || strings.Contains(card, "Zimmer") {
			t.Errorf("card %q", card)
		}
	})

	t.Run("a request the admin never saw is taken back", func(t *testing.T) {
		bot, db := &fakeBot{sendErr: errors.New("chat not found")}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err == nil {
			t.Error("want the error for the log")
		}
		if db.rows[userID].Status != users.StatusNone {
			t.Errorf("status %q, want none so /start can be tried again", db.rows[userID].Status)
		}
	})

	t.Run("a banned user gets nothing", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusKicked); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 0 || len(db.rows) != 0 {
			t.Errorf("sent %+v, rows %v", bot.sent, db.rows)
		}
	})
}

func press(chatID int64, from int64, data string) *telegram.CallbackQuery {
	return &telegram.CallbackQuery{
		ID:      "cb",
		From:    &telegram.User{ID: from},
		Message: &telegram.Message{MessageID: 9, Chat: &telegram.Chat{ID: chatID}, Text: "card"},
		Data:    data,
	}
}

func TestCallbackDecision(t *testing.T) {
	ctx := context.Background()
	data := func(act string) string { return fmt.Sprintf("%s:%d", act, userID) }

	t.Run("approve sends the room keyboard", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Callback(ctx, press(adminID, adminID, data(actApprove))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusApproved {
			t.Errorf("status %q", db.rows[userID].Status)
		}
		if got := bot.last(); got.chatID != userID || len(got.rows) == 0 {
			t.Errorf("got %+v", got)
		}
		if len(bot.edits) != 1 || !strings.Contains(bot.edits[0], "Angenommen") {
			t.Errorf("card not retired: %v", bot.edits)
		}
		if len(bot.answers) != 1 {
			t.Errorf("press answered %d times", len(bot.answers))
		}
	})

	t.Run("reject tells the user once", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Callback(ctx, press(adminID, adminID, data(actReject))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusRejected {
			t.Errorf("status %q", db.rows[userID].Status)
		}
		if got := bot.last(); got.chatID != userID || got.text != msgRejected {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("a second press changes nothing", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusRejected}})

		if err := flow(bot, db).Callback(ctx, press(adminID, adminID, data(actApprove))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusRejected || len(bot.sent) != 0 {
			t.Errorf("status %q, sent %+v", db.rows[userID].Status, bot.sent)
		}
		if bot.answers[0] != msgDone {
			t.Errorf("answer %q", bot.answers[0])
		}
	})

	t.Run("a group admin in an admin group decides, and the card names them", func(t *testing.T) {
		bot := &fakeBot{roles: map[int64]telegram.MemberStatus{77: telegram.StatusAdministrator}}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})
		f := flow(bot, db)
		f.AdminID = -100500
		q := press(f.AdminID, 77, data(actApprove))
		q.From.FirstName = "Lena"

		if err := f.Callback(ctx, q); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusApproved {
			t.Errorf("status %q", db.rows[userID].Status)
		}
		if len(bot.edits) != 1 || !strings.HasSuffix(bot.edits[0], "Angenommen von Lena") {
			t.Errorf("card %v", bot.edits)
		}
	})

	t.Run("a group admin's press counts in any chat", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Callback(ctx, press(adminID+1, adminID, data(actApprove))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusApproved {
			t.Errorf("status %q", db.rows[userID].Status)
		}
	})

	t.Run("a plain member of the admin chat is refused", func(t *testing.T) {
		bot := &fakeBot{roles: map[int64]telegram.MemberStatus{77: telegram.StatusMember}}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Callback(ctx, press(adminID, 77, data(actApprove))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusPending || bot.answers[0] != msgNotAllowed {
			t.Errorf("status %q, answer %q", db.rows[userID].Status, bot.answers[0])
		}
	})

	t.Run("the newcomer can't approve themselves", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Callback(ctx, press(userID, userID, data(actApprove))); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusPending {
			t.Errorf("status %q, want still pending", db.rows[userID].Status)
		}
		if bot.answers[0] != msgNotAllowed {
			t.Errorf("answer %q", bot.answers[0])
		}
	})
}

func TestCallbackRoom(t *testing.T) {
	ctx := context.Background()

	t.Run("an approved user stores the room and gets the link", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved}})

		if err := flow(bot, db).Callback(ctx, press(userID, userID, "room:3")); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Room != 3 {
			t.Errorf("room %d", db.rows[userID].Room)
		}
		if bot.links != 1 || db.rows[userID].InviteLink != "https://t.me/+link1" {
			t.Errorf("links %d, stored %q", bot.links, db.rows[userID].InviteLink)
		}
	})

	t.Run("buttons can't skip the admin", func(t *testing.T) {
		for _, st := range []users.Status{users.StatusNone, users.StatusPending, users.StatusRejected} {
			bot := &fakeBot{}
			db := newDB(map[int64]users.User{userID: {Status: st}})

			if err := flow(bot, db).Callback(ctx, press(userID, userID, "room:3")); err != nil {
				t.Fatal(err)
			}
			if db.rows[userID].Room != 0 || bot.links != 0 {
				t.Errorf("status %q: room %d, links %d", st, db.rows[userID].Room, bot.links)
			}
		}
	})

	t.Run("only rooms 1 to 8, only in the presser's own chat", func(t *testing.T) {
		cases := []struct {
			name string
			q    *telegram.CallbackQuery
		}{
			{"room 0", press(userID, userID, "room:0")},
			{"room 9", press(userID, userID, "room:9")},
			{"negative", press(userID, userID, "room:-1")},
			{"not a number", press(userID, userID, "room:drei")},
			{"leading plus", press(userID, userID, "room:+3")},
			{"unknown action", press(userID, userID, "admin:3")},
			{"pressed in another chat", press(groupID, userID, "room:3")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				bot := &fakeBot{}
				db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved}})

				if err := flow(bot, db).Callback(ctx, tc.q); err != nil {
					t.Fatal(err)
				}
				if db.rows[userID].Room != 0 || bot.links != 0 {
					t.Errorf("room %d, links %d", db.rows[userID].Room, bot.links)
				}
				if len(bot.answers) != 1 || bot.answers[0] != msgNotAllowed {
					t.Errorf("answers %v", bot.answers)
				}
			})
		}
	})
}

func TestJoinRequest(t *testing.T) {
	ctx := context.Background()
	const link = "https://t.me/+issued"
	request := func(chatID int64) *telegram.ChatJoinRequest {
		return &telegram.ChatJoinRequest{
			Chat: &telegram.Chat{ID: chatID}, From: newcomer, UserChatID: userID,
			InviteLink: &telegram.ChatInviteLink{InviteLink: link},
		}
	}
	approvedRow := users.User{Status: users.StatusApproved, Room: 4, InviteLink: link}

	t.Run("an approved user with a room is let in, tagged and greeted", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: approvedRow})

		if err := flow(bot, db).JoinRequest(ctx, request(groupID)); err != nil {
			t.Fatal(err)
		}
		if len(bot.approved) != 1 || bot.tags[userID] != "4" {
			t.Errorf("approved %v, tags %v", bot.approved, bot.tags)
		}
		if got := bot.last(); got.chatID != groupID || !strings.Contains(got.text, "Anna Muster") || !strings.Contains(got.text, "Zimmer 4") {
			t.Errorf("greeting %+v", got)
		}
		if got := db.rows[userID]; got.Status != users.StatusNone || got.InviteLink != "" || got.JoinedAt.IsZero() {
			t.Errorf("row after joining: %+v", got)
		}
		if len(bot.revoked) != 1 || bot.revoked[0] != link {
			t.Errorf("revoked %v", bot.revoked)
		}
	})

	t.Run("a former member is welcomed back", func(t *testing.T) {
		bot := &fakeBot{}
		row := approvedRow
		row.JoinedAt = time.Now().Add(-30 * 24 * time.Hour)
		db := newDB(map[int64]users.User{userID: row})

		if err := flow(bot, db).JoinRequest(ctx, request(groupID)); err != nil {
			t.Fatal(err)
		}
		if got := bot.last().text; got != "👋 Anna Muster ist wieder da! Zimmer 4." {
			t.Errorf("greeting %q", got)
		}
	})

	t.Run("only through the link issued to that user", func(t *testing.T) {
		for name, il := range map[string]*telegram.ChatInviteLink{
			"another link": {InviteLink: "https://t.me/+someone-elses"},
			"no link":      nil,
		} {
			bot := &fakeBot{}
			db := newDB(map[int64]users.User{userID: approvedRow})
			r := request(groupID)
			r.InviteLink = il

			if err := flow(bot, db).JoinRequest(ctx, r); err != nil {
				t.Fatal(err)
			}
			if len(bot.approved) != 0 {
				t.Errorf("%s: approved %v", name, bot.approved)
			}
		}
	})

	t.Run("anybody else is left for a human", func(t *testing.T) {
		rows := []users.User{
			{},
			{Status: users.StatusPending},
			{Status: users.StatusRejected, Room: 2},
			{Status: users.StatusApproved, InviteLink: link}, // no room yet
		}
		for _, u := range rows {
			bot := &fakeBot{}
			db := newDB(map[int64]users.User{userID: u})

			if err := flow(bot, db).JoinRequest(ctx, request(groupID)); err != nil {
				t.Fatal(err)
			}
			if len(bot.approved) != 0 || len(bot.sent) != 0 {
				t.Errorf("%+v: approved %v, sent %+v", u, bot.approved, bot.sent)
			}
		}
	})

	t.Run("another chat's requests are none of our business", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: approvedRow})

		if err := flow(bot, db).JoinRequest(ctx, request(-1)); err != nil {
			t.Fatal(err)
		}
		if len(bot.approved) != 0 {
			t.Errorf("approved %v", bot.approved)
		}
	})
}

func TestRoomKeyboardOffersEveryRoomOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range roomKeyboard() {
		for _, b := range r {
			act, n, ok := parseData(b.Data)
			if !ok || act != actRoom || b.Text != Tag(int(n)) || seen[b.Data] {
				t.Errorf("button %+v", b)
			}
			seen[b.Data] = true
		}
	}
	if len(seen) != 8 {
		t.Errorf("got %d rooms, want 8", len(seen))
	}
}

func TestCallbackChangeRoom(t *testing.T) {
	ctx := context.Background()
	change := fmt.Sprintf("%s:%d", actChange, userID)
	live := func() users.User {
		return users.User{Status: users.StatusApproved, Room: 3, InviteLink: "https://t.me/+old", InviteExpires: time.Now().Add(LinkTTL)}
	}

	t.Run("an approved user picks again and keeps their link", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(map[int64]users.User{userID: live()})
		f := flow(bot, db)

		if err := f.Callback(ctx, press(userID, userID, change)); err != nil {
			t.Fatal(err)
		}
		if got := bot.last(); got.chatID != userID || len(got.rows) != 2 {
			t.Fatalf("got %+v, want the room keyboard", got)
		}
		if err := f.Callback(ctx, press(userID, userID, "room:5")); err != nil {
			t.Fatal(err)
		}
		got := bot.last()
		if db.rows[userID].Room != 5 || bot.links != 0 || len(bot.revoked) != 0 {
			t.Errorf("room %d, links %d, revoked %v", db.rows[userID].Room, bot.links, bot.revoked)
		}
		if !strings.Contains(got.text, "Dein Zimmer: 5.") || !strings.Contains(got.text, "https://t.me/+old") {
			t.Errorf("link message %q", got.text)
		}
		if len(got.rows) != 1 || got.rows[0][0].Data != change {
			t.Errorf("link message buttons %+v, want Zimmer ändern", got.rows)
		}
	})

	t.Run("someone else's button is refused", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(map[int64]users.User{userID: live()})

		if err := flow(bot, db).Callback(ctx, press(userID+1, userID+1, change)); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 0 || bot.answers[0] != msgNotAllowed {
			t.Errorf("sent %+v, answer %q", bot.sent, bot.answers[0])
		}
	})

	t.Run("after joining the button does nothing", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(map[int64]users.User{userID: {Room: 3}})

		if err := flow(bot, db).Callback(ctx, press(userID, userID, change)); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 0 || bot.answers[0] != msgNotAllowed {
			t.Errorf("sent %+v, answer %q", bot.sent, bot.answers[0])
		}
	})
}

func TestMemberUpdate(t *testing.T) {
	ctx := context.Background()
	anna := &telegram.User{ID: userID, FirstName: "Anna", Username: "anna"}
	change := func(chat int64, from *telegram.User, oldStatus, newStatus string) *telegram.ChatMemberUpdated {
		return &telegram.ChatMemberUpdated{
			Chat: &telegram.Chat{ID: chat}, From: from,
			Old: telegram.ChatMember{User: anna, Status: oldStatus},
			New: telegram.ChatMember{User: anna, Status: newStatus},
		}
	}

	t.Run("a member who leaves is noted and the admins hear of it", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(map[int64]users.User{userID: {Room: 3, JoinedAt: time.Now()}})

		if err := flow(bot, db).MemberUpdate(ctx, change(groupID, anna, "member", "left")); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].LeftAt.IsZero() {
			t.Error("left_at not set")
		}
		got := bot.last()
		if got.chatID != adminID || got.text != "Anna (@anna) hat die WG-Gruppe verlassen.\nZimmer 3 – falls es frei wird, den Putzplan anpassen." {
			t.Errorf("notice %+v", got)
		}
	})

	t.Run("a member the bot never saw, removed by an admin", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(nil)

		if err := flow(bot, db).MemberUpdate(ctx, change(groupID, &telegram.User{ID: adminID}, "member", "kicked")); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].LeftAt.IsZero() || !db.rows[userID].FormerMember() {
			t.Errorf("row %+v", db.rows[userID])
		}
		if got := bot.last().text; got != "Anna (@anna) wurde aus der WG-Gruppe entfernt." {
			t.Errorf("notice %q", got)
		}
	})

	for name, u := range map[string]*telegram.ChatMemberUpdated{
		"a join":          change(groupID, anna, "left", "member"),
		"another chat":    change(groupID+1, anna, "member", "left"),
		"a promotion":     change(groupID, anna, "member", "administrator"),
		"a muted member":  {Chat: &telegram.Chat{ID: groupID}, Old: telegram.ChatMember{User: anna, Status: "member"}, New: telegram.ChatMember{User: anna, Status: "restricted", IsMember: true}},
		"a bot that left": {Chat: &telegram.Chat{ID: groupID}, Old: telegram.ChatMember{User: &telegram.User{ID: 5, IsBot: true}, Status: "member"}, New: telegram.ChatMember{User: &telegram.User{ID: 5, IsBot: true}, Status: "left"}},
	} {
		t.Run(name+" is ignored", func(t *testing.T) {
			bot, db := &fakeBot{}, newDB(nil)

			if err := flow(bot, db).MemberUpdate(ctx, u); err != nil {
				t.Fatal(err)
			}
			if len(bot.sent) != 0 || len(db.rows) != 0 {
				t.Errorf("sent %+v, rows %v", bot.sent, db.rows)
			}
		})
	}
}

func TestFormerMemberSeenLeaving(t *testing.T) {
	ctx := context.Background()
	left := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	t.Run("the card gives the date they left", func(t *testing.T) {
		bot, db := &fakeBot{}, newDB(map[int64]users.User{userID: {LeftAt: left}})

		if err := flow(bot, db).Start(ctx, newcomer, telegram.StatusLeft); err != nil {
			t.Fatal(err)
		}
		if card := bot.sent[0].text; !strings.HasSuffix(card, "War schon Mitglied der WG, ausgetreten am 02.11.2026.") {
			t.Errorf("card %q", card)
		}
	})

	t.Run("joining again clears left_at and they are welcomed back", func(t *testing.T) {
		const link = "https://t.me/+link"
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 2, InviteLink: link, LeftAt: left}})
		r := &telegram.ChatJoinRequest{
			Chat: &telegram.Chat{ID: groupID}, From: newcomer, UserChatID: userID,
			InviteLink: &telegram.ChatInviteLink{InviteLink: link},
		}

		if err := flow(bot, db).JoinRequest(ctx, r); err != nil {
			t.Fatal(err)
		}
		if !db.rows[userID].LeftAt.IsZero() || db.rows[userID].JoinedAt.IsZero() {
			t.Errorf("row %+v", db.rows[userID])
		}
		if got := bot.last().text; got != "👋 Anna Muster ist wieder da! Zimmer 2." {
			t.Errorf("greeting %q", got)
		}
	})
}
