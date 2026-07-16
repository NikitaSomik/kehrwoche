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
