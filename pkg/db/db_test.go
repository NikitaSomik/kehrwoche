package db

import (
	"context"
	"testing"
)

func TestConnectEmptyURL(t *testing.T) {
	_, err := Connect(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error for an empty URL, got nil")
	}
}
