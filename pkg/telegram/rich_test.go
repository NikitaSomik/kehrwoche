package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// scriptedClient answers the n-th request with replies[n] and records each
// method and body.
func scriptedClient(replies ...string) (*Client, *[]string) {
	var calls []string
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]+" "+string(b))
		reply := replies[min(len(calls), len(replies))-1]
		status := http.StatusOK
		if strings.Contains(reply, `"ok":false`) {
			status = http.StatusBadRequest
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})}
	return New("T", WithHTTPClient(hc)), &calls
}

func TestSendRich(t *testing.T) {
	m := Rich{HTML: "<h3>Plan</h3>", Plain: "Plan"}

	t.Run("sends the html", func(t *testing.T) {
		client, calls := scriptedClient(okBody)
		if err := client.SendRich(context.Background(), -100, m); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || (*calls)[0] != `sendRichMessage {"chat_id":-100,"rich_message":{"html":"\u003ch3\u003ePlan\u003c/h3\u003e"}}` {
			t.Errorf("calls %q", *calls)
		}
	})

	t.Run("a refusal falls back to plain text", func(t *testing.T) {
		client, calls := scriptedClient(`{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`, okBody)
		if err := client.SendRich(context.Background(), -100, m); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 2 || (*calls)[1] != `sendMessage {"chat_id":-100,"text":"Plan"}` {
			t.Errorf("calls %q", *calls)
		}
	})

	// After a timeout the rich message may have landed; a plain copy could
	// duplicate it.
	t.Run("a transport error does not fall back", func(t *testing.T) {
		client := fakeClient(0, "", errors.New("timeout"))
		if err := client.SendRich(context.Background(), -100, m); err == nil {
			t.Error("want the error")
		}
	})

	t.Run("a server error does not fall back", func(t *testing.T) {
		client, calls := scriptedClient(`{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
		if err := client.SendRich(context.Background(), -100, m); err == nil || len(*calls) != 1 {
			t.Errorf("err %v, calls %q", err, *calls)
		}
	})
}

func TestHTMLTableEscapes(t *testing.T) {
	got := HTMLTable([]string{"A&B"}, [][]string{{"<x>"}})
	want := "<table><tr><th>A&amp;B</th></tr><tr><td>&lt;x&gt;</td></tr></table>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
