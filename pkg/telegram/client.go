package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout bounds one Bot API call when New is not given an HTTP client.
// Callers' contexts usually end sooner; this is the backstop for one that has
// no deadline.
const DefaultTimeout = 10 * time.Second

const (
	defaultBaseURL = "https://api.telegram.org"
	// Bot API responses are small JSON; never trust a response size blindly.
	maxResponseBytes = 1 << 16
)

// Client calls the Bot API for one bot. Every Bot API method the project uses
// is a method on it. The token stays unexported, so it can't be printed with
// the value and never appears in an error.
type Client struct {
	http    *http.Client
	token   string
	baseURL string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client, e.g. to record requests in tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// New returns a Client for the bot with the given token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		http:    &http.Client{Timeout: DefaultTimeout},
		token:   token,
		baseURL: defaultBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is a call the Bot API answered with a failure, as opposed to one
// that never got an answer (a transport error or timeout). The difference
// matters to a caller that wants to retry: an APIError means the request was
// refused, so nothing was sent, while after a timeout a message may well have
// been delivered.
type APIError struct {
	Method      string
	Code        int    // error_code from the response, or the HTTP status if it had none
	Description string // e.g. "Bad Request: chat not found"
	// RetryAfter is set on 429 Too Many Requests: how long to wait before
	// the next call.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s: %d %s", e.Method, e.Code, e.Description)
}

// response is the envelope every Bot API answer comes in.
type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call POSTs payload as JSON to a Bot API method and decodes the result into
// out, unless out is nil. A refusal comes back as *APIError. Errors never
// include the bot token.
func (c *Client) call(ctx context.Context, method string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: %s: encode: %w", method, err)
	}

	endpoint := c.baseURL + "/bot" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		// The error of a malformed URL quotes it, token and all.
		return fmt.Errorf("telegram: %s: build request failed", method)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// http.Client wraps transport failures in a *url.Error whose Error()
		// includes the full request URL — and thus the bot token. Unwrap to
		// the underlying error before it reaches logs.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("telegram: %s failed: %w", method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("telegram: %s: read response: %w", method, err)
	}

	var env response
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode != http.StatusOK {
			// Not the Bot API speaking, e.g. a proxy's error page.
			return &APIError{Method: method, Code: resp.StatusCode, Description: string(bytes.TrimSpace(raw))}
		}
		return fmt.Errorf("telegram: %s: decode response: %w", method, err)
	}
	if !env.OK || resp.StatusCode != http.StatusOK {
		apiErr := &APIError{
			Method:      method,
			Code:        env.ErrorCode,
			Description: env.Description,
			RetryAfter:  time.Duration(env.Parameters.RetryAfter) * time.Second,
		}
		if apiErr.Code == 0 {
			apiErr.Code = resp.StatusCode
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("telegram: %s: decode result: %w", method, err)
	}
	return nil
}
