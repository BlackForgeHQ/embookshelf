package auth

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestUserFromContextEmpty(t *testing.T) {
	if u := UserFromContext(context.Background()); u != nil {
		t.Errorf("expected nil user, got %+v", u)
	}
}

func TestWithUserNilReturnsSameContext(t *testing.T) {
	parent := context.Background()
	got := WithUser(parent, nil)
	if got != parent {
		t.Error("WithUser(nil) should return the parent context unchanged")
	}
	if u := UserFromContext(got); u != nil {
		t.Errorf("expected nil user, got %+v", u)
	}
}

func TestWithUserRoundTrip(t *testing.T) {
	want := &model.User{ID: "u1", Email: "x@y.z", Role: model.RoleAdmin}
	ctx := WithUser(context.Background(), want)
	got := UserFromContext(ctx)
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got != want {
		t.Errorf("expected same pointer, got different (%+v vs %+v)", got, want)
	}
}
