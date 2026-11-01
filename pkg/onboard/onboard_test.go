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
// statement by a fragment of its SQL. The real SQL is covered by
// pkg/users' integration tests; this keeps the flow's own logic testable.
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
	case strings.Contains(sql, "INSERT INTO users (tg_user_id, status)"):
		if !found || u.Status == users.StatusNone {
			u.Status = users.StatusPending
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
	case strings.Contains(sql, "INSERT INTO users (tg_user_id, room)"):
		u.Room = args[1].(int)
		affected = true
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
	return nil
}

type sent struct {
	chatID int64
	text   string
	rows   [][]telegram.Button
}

type fakeBot struct {
	member    bool
	memberErr error
	sendErr   error

	sent     []sent
	edits    []string
	answers  []string
	tags     map[int64]string
	approved []int64
	links    int
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

func (b *fakeBot) IsMember(context.Context, int64, int64) (bool, error) {
	return b.member, b.memberErr
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
	return "https://t.me/+abc", nil
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

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if db.rows[userID].Status != users.StatusPending {
			t.Errorf("status %q, want pending", db.rows[userID].Status)
		}
		if len(bot.sent) != 2 {
			t.Fatalf("got %d messages, want the admin card and the user's confirmation", len(bot.sent))
		}
		card := bot.sent[0]
		if card.chatID != adminID || !strings.Contains(card.text, "Anna Muster (@anna)") || len(card.rows) != 1 {
			t.Errorf("admin card: %+v", card)
		}
		if bot.sent[1].chatID != userID || bot.sent[1].text != msgRequestSent {
			t.Errorf("user reply: %+v", bot.sent[1])
		}
	})

	t.Run("a pending user is told to wait and the admin is not asked again", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusPending}})

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 1 || bot.sent[0].chatID != userID || bot.sent[0].text != msgPending {
			t.Errorf("got %+v", bot.sent)
		}
	})

	t.Run("a rejected user gets no answer at all", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusRejected}})

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 0 {
			t.Errorf("got %+v, want silence", bot.sent)
		}
	})

	t.Run("an approved user without a room is asked again", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved}})

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if len(bot.last().rows) == 0 {
			t.Errorf("got %+v, want the room keyboard", bot.sent)
		}
	})

	t.Run("an approved user with a room gets a fresh link", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 3}})

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if bot.links != 1 || !strings.Contains(bot.last().text, "https://t.me/+abc") {
			t.Errorf("got %+v", bot.sent)
		}
	})

	t.Run("a member skips the admin and picks a room", func(t *testing.T) {
		bot, db := &fakeBot{member: true}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer); err != nil {
			t.Fatal(err)
		}
		if len(bot.sent) != 1 || bot.sent[0].chatID != userID || len(bot.sent[0].rows) == 0 {
			t.Errorf("got %+v, want only the room keyboard", bot.sent)
		}
		if _, found := db.rows[userID]; found {
			t.Error("a member's /start must not file a request")
		}
	})

	t.Run("a request the admin never saw is taken back", func(t *testing.T) {
		bot, db := &fakeBot{sendErr: errors.New("chat not found")}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer); err == nil {
			t.Error("want the error for the log")
		}
		if db.rows[userID].Status != users.StatusNone {
			t.Errorf("status %q, want none so /start can be tried again", db.rows[userID].Status)
		}
	})

	t.Run("a failed membership check files nothing", func(t *testing.T) {
		bot, db := &fakeBot{memberErr: errors.New("timeout")}, newDB(nil)

		if err := flow(bot, db).Start(ctx, newcomer); err == nil {
			t.Error("want the error for the log")
		}
		if len(db.rows) != 0 {
			t.Errorf("got rows %v", db.rows)
		}
		if bot.last().text != msgTryLater {
			t.Errorf("got %+v, want the try-later reply", bot.sent)
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

	t.Run("a press from outside the admin chat is refused", func(t *testing.T) {
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
		if bot.links != 1 {
			t.Errorf("links %d", bot.links)
		}
	})

	t.Run("a member's room becomes their tag", func(t *testing.T) {
		bot, db := &fakeBot{member: true}, newDB(nil)

		if err := flow(bot, db).Callback(ctx, press(userID, userID, "room:5")); err != nil {
			t.Fatal(err)
		}
		if bot.tags[userID] != "5" {
			t.Errorf("tag %q, want the bare number", bot.tags[userID])
		}
		if db.rows[userID].Room != 5 || bot.links != 0 {
			t.Errorf("room %d, links %d", db.rows[userID].Room, bot.links)
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
				bot := &fakeBot{member: true}
				db := newDB(nil)

				if err := flow(bot, db).Callback(ctx, tc.q); err != nil {
					t.Fatal(err)
				}
				if len(bot.tags) != 0 || len(db.rows) != 0 {
					t.Errorf("tags %v, rows %v", bot.tags, db.rows)
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
	request := func(chatID int64) *telegram.ChatJoinRequest {
		return &telegram.ChatJoinRequest{Chat: &telegram.Chat{ID: chatID}, From: newcomer, UserChatID: userID}
	}

	t.Run("an approved user with a room is let in, tagged and greeted", func(t *testing.T) {
		bot := &fakeBot{}
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 4}})

		if err := flow(bot, db).JoinRequest(ctx, request(groupID)); err != nil {
			t.Fatal(err)
		}
		if len(bot.approved) != 1 || bot.tags[userID] != "4" {
			t.Errorf("approved %v, tags %v", bot.approved, bot.tags)
		}
		if got := bot.last(); got.chatID != groupID || !strings.Contains(got.text, "Anna Muster") || !strings.Contains(got.text, "Zimmer 4") {
			t.Errorf("greeting %+v", got)
		}
	})

	t.Run("anybody else is left for a human", func(t *testing.T) {
		rows := []users.User{
			{},
			{Status: users.StatusPending},
			{Status: users.StatusRejected, Room: 2},
			{Status: users.StatusApproved}, // no room yet
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
		db := newDB(map[int64]users.User{userID: {Status: users.StatusApproved, Room: 4}})

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
