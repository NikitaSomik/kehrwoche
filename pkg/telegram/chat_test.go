package telegram

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsMember(t *testing.T) {
	cases := []struct {
		result string
		want   bool
	}{
		{`{"status":"creator"}`, true},
		{`{"status":"administrator"}`, true},
		{`{"status":"member","tag":"3"}`, true},
		{`{"status":"restricted","is_member":true}`, true},
		{`{"status":"restricted","is_member":false}`, false},
		{`{"status":"left"}`, false},
		{`{"status":"kicked"}`, false},
	}
	for _, tc := range cases {
		client, body, path := capturingClient(http.StatusOK, `{"ok":true,"result":`+tc.result+`}`)
		got, err := IsMember(context.Background(), client, "T", -100, 7)
		if err != nil {
			t.Fatalf("%s: %v", tc.result, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.result, got, tc.want)
		}
		if !strings.HasSuffix(*path, "/getChatMember") || !strings.Contains(*body, `"user_id":7`) {
			t.Errorf("request %s %s", *path, *body)
		}
	}
}

// A user who never was in the chat comes back as an error from Telegram, not
// as "left"; the caller must see it rather than a silent false.
func TestIsMemberError(t *testing.T) {
	client := fakeClient(http.StatusBadRequest, `{"ok":false,"description":"Bad Request: user not found"}`, nil)
	if _, err := IsMember(context.Background(), client, "T", -100, 7); err == nil {
		t.Error("want an error")
	}
}

func TestSendKeyboard(t *testing.T) {
	client, body, _ := capturingClient(http.StatusOK, `{"ok":true}`)
	rows := [][]Button{{{Text: "1", Data: "room:1"}}}
	if err := SendKeyboard(context.Background(), client, "T", 7, "Zimmer?", rows); err != nil {
		t.Fatal(err)
	}
	want := `"reply_markup":{"inline_keyboard":[[{"text":"1","callback_data":"room:1"}]]}`
	if !strings.Contains(*body, want) {
		t.Errorf("got %s, want it to contain %s", *body, want)
	}
	if strings.Contains(*body, "parse_mode") {
		t.Errorf("keyboard messages are plain text: %s", *body)
	}
}

func TestSetMemberTag(t *testing.T) {
	client, body, path := capturingClient(http.StatusOK, `{"ok":true,"result":true}`)
	if err := SetMemberTag(context.Background(), client, "T", -100, 7, "3"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(*path, "/setChatMemberTag") || !strings.Contains(*body, `"tag":"3"`) {
		t.Errorf("request %s %s", *path, *body)
	}
}

func TestCreateJoinRequestLink(t *testing.T) {
	client, body, _ := capturingClient(http.StatusOK, `{"ok":true,"result":{"invite_link":"https://t.me/+xyz","creates_join_request":true}}`)
	expire := time.Unix(1800000000, 0)
	link, err := CreateJoinRequestLink(context.Background(), client, "T", -100, "Onboarding 7", expire)
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://t.me/+xyz" {
		t.Errorf("link %q", link)
	}
	for _, want := range []string{`"creates_join_request":true`, `"expire_date":1800000000`, `"name":"Onboarding 7"`} {
		if !strings.Contains(*body, want) {
			t.Errorf("payload %s lacks %s", *body, want)
		}
	}
	// member_limit can't be combined with creates_join_request.
	if strings.Contains(*body, "member_limit") {
		t.Errorf("payload sets member_limit: %s", *body)
	}
}

func TestCreateJoinRequestLinkEmpty(t *testing.T) {
	client := fakeClient(http.StatusOK, `{"ok":true,"result":{}}`, nil)
	if _, err := CreateJoinRequestLink(context.Background(), client, "T", -100, "x", time.Now()); err == nil {
		t.Error("want an error for a response without a link")
	}
}

func TestSetWebhook(t *testing.T) {
	client, body, _ := capturingClient(http.StatusOK, `{"ok":true,"result":true}`)
	err := SetWebhook(context.Background(), client, "T", "https://x/api/webhook", "s3cret", []string{"message", "callback_query"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"secret_token":"s3cret"`, `"allowed_updates":["message","callback_query"]`} {
		if !strings.Contains(*body, want) {
			t.Errorf("payload %s lacks %s", *body, want)
		}
	}
}
