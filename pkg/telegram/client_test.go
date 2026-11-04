package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	c := New("T")
	if c.http == http.DefaultClient {
		t.Error("New must not share http.DefaultClient, which has no timeout")
	}
	if c.http.Timeout != DefaultTimeout {
		t.Errorf("timeout %v, want %v", c.http.Timeout, DefaultTimeout)
	}

	hc := &http.Client{}
	if got := New("T", WithHTTPClient(hc)).http; got != hc {
		t.Error("WithHTTPClient was ignored")
	}
}

func TestCallURL(t *testing.T) {
	var got string
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return fakeTransport(http.StatusOK, okBody, nil).Transport.RoundTrip(r)
	})}
	if err := New("123:abc", WithHTTPClient(hc)).SendPlain(context.Background(), 1, "hi"); err != nil {
		t.Fatal(err)
	}
	if want := "https://api.telegram.org/bot123:abc/sendMessage"; got != want {
		t.Errorf("url %q, want %q", got, want)
	}
}

func TestCallErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    error
		want   *APIError // nil: the error must not be an *APIError
		reason string    // a substring the error must contain
	}{
		{
			name:   "a refusal carries Telegram's code and description",
			status: http.StatusBadRequest,
			body:   `{"ok":false,"error_code":400,"description":"Bad Request: BUTTON_USER_PRIVACY_RESTRICTED"}`,
			want:   &APIError{Method: "sendMessage", Code: 400, Description: "Bad Request: BUTTON_USER_PRIVACY_RESTRICTED"},
			reason: "BUTTON_USER_PRIVACY_RESTRICTED",
		},
		{
			name:   "flood control says when to try again",
			status: http.StatusTooManyRequests,
			body:   `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`,
			want:   &APIError{Method: "sendMessage", Code: 429, Description: "Too Many Requests: retry after 7", RetryAfter: 7 * time.Second},
			reason: "429",
		},
		{
			name:   "a body that isn't the Bot API's falls back to the HTTP status",
			status: http.StatusBadGateway,
			body:   "<html>502 Bad Gateway</html>\n",
			want:   &APIError{Method: "sendMessage", Code: 502, Description: "<html>502 Bad Gateway</html>"},
			reason: "502",
		},
		{
			name:   "ok:false without an error_code takes the HTTP status",
			status: http.StatusUnauthorized,
			body:   `{"ok":false,"description":"Unauthorized"}`,
			want:   &APIError{Method: "sendMessage", Code: 401, Description: "Unauthorized"},
			reason: "Unauthorized",
		},
		{
			name:   "an unreadable 200 is a decode error, not a refusal",
			status: http.StatusOK,
			body:   "",
			reason: "decode response",
		},
		{
			name:   "a timeout is not a refusal: the message may have been delivered",
			err:    context.DeadlineExceeded,
			reason: "deadline exceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fakeClient(tc.status, tc.body, tc.err).SendPlain(context.Background(), 1, "hi")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q lacks %q", err, tc.reason)
			}
			var apiErr *APIError
			isAPI := errors.As(err, &apiErr)
			switch {
			case tc.want == nil && isAPI:
				t.Errorf("got *APIError %+v, want another error", apiErr)
			case tc.want != nil && !isAPI:
				t.Errorf("got %T %v, want *APIError", err, err)
			case tc.want != nil && *apiErr != *tc.want:
				t.Errorf("got %+v, want %+v", *apiErr, *tc.want)
			}
		})
	}
}

func TestCallDecodesResult(t *testing.T) {
	c := fakeClient(http.StatusOK, `{"ok":true,"result":{"url":"https://x/api/webhook","pending_update_count":2}}`, nil)
	info, err := c.GetWebhookInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://x/api/webhook" || info.PendingUpdateCount != 2 {
		t.Errorf("got %+v", info)
	}
}

func TestCallWrongResultShape(t *testing.T) {
	c := fakeClient(http.StatusOK, `{"ok":true,"result":true}`, nil)
	if _, err := c.GetWebhookInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "decode result") {
		t.Errorf("got %v, want a decode error", err)
	}
}
