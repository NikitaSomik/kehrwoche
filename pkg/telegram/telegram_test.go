package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeClient(status int, body string, err error) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
}

// capturingClient records the request body and path of the last request.
func capturingClient(status int, respBody string) (*http.Client, *string, *string) {
	var gotBody, gotPath string
	c := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			gotPath = r.URL.Path
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(respBody))}, nil
		}),
	}
	return c, &gotBody, &gotPath
}

func TestSendSuccess(t *testing.T) {
	client := fakeClient(http.StatusOK, "", nil)
	if err := Send(context.Background(), client, "TOKEN", 1, "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendNonOKIncludesBody(t *testing.T) {
	client := fakeClient(http.StatusBadRequest, `{"description":"chat not found"}`, nil)
	err := Send(context.Background(), client, "TOKEN", 1, "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("got %v, want error containing the response body", err)
	}
}

func TestSendTransportErrorDoesNotLeakToken(t *testing.T) {
	client := fakeClient(0, "", errors.New("connection refused"))
	err := Send(context.Background(), client, "super-secret-token", 1, "hi")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the bot token: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error lost the underlying cause: %v", err)
	}
}

func TestSendUsesMarkdown(t *testing.T) {
	client, body, path := capturingClient(http.StatusOK, "")
	if err := Send(context.Background(), client, "T", 1, "hi"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(*path, "/sendMessage") {
		t.Errorf("path = %q, want .../sendMessage", *path)
	}
	if !strings.Contains(*body, `"parse_mode":"Markdown"`) {
		t.Errorf("Send did not set Markdown parse_mode: %s", *body)
	}
}

func TestSendPlainOmitsParseMode(t *testing.T) {
	client, body, _ := capturingClient(http.StatusOK, "")
	if err := SendPlain(context.Background(), client, "T", 1, "/etage_plan"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(*body, "parse_mode") {
		t.Errorf("SendPlain set a parse_mode: %s", *body)
	}
	if !strings.Contains(*body, "/etage_plan") {
		t.Errorf("body lost the text: %s", *body)
	}
}

func TestSetCommandsDefaultScope(t *testing.T) {
	client, body, path := capturingClient(http.StatusOK, `{"ok":true,"result":true}`)
	err := SetCommands(context.Background(), client, "T", []Command{
		{Command: "help", Description: "Alle Befehle"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(*path, "/setMyCommands") {
		t.Errorf("path = %q, want .../setMyCommands", *path)
	}
	if !strings.Contains(*body, `"command":"help"`) || !strings.Contains(*body, `"description":"Alle Befehle"`) {
		t.Errorf("unexpected setMyCommands payload: %s", *body)
	}
	if strings.Contains(*body, "scope") {
		t.Errorf("default scope must not send a scope field: %s", *body)
	}
}

func TestSetCommandsWithScope(t *testing.T) {
	client, body, _ := capturingClient(http.StatusOK, `{"ok":true,"result":true}`)
	err := SetCommands(context.Background(), client, "T", nil, "all_group_chats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*body, `"scope":{"type":"all_group_chats"}`) {
		t.Errorf("scope not in payload: %s", *body)
	}
}

func TestGetCommandsParsesResult(t *testing.T) {
	resp := `{"ok":true,"result":[{"command":"help","description":"Alle Befehle"},{"command":"etage","description":"Etage"}]}`
	client, body, _ := capturingClient(http.StatusOK, resp)
	cmds, err := GetCommands(context.Background(), client, "T", "all_private_chats")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0].Command != "help" || cmds[1].Description != "Etage" {
		t.Errorf("got %+v", cmds)
	}
	if !strings.Contains(*body, `"scope":{"type":"all_private_chats"}`) {
		t.Errorf("scope not in getMyCommands payload: %s", *body)
	}
}
