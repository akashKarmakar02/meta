package action

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestValidationSnapshotAndFormState(t *testing.T) {
	fields := map[string][]string{"user.name": {"required", "too short"}}
	e := NewValidationError("Please correct", fields)
	fields["user.name"][0] = "changed"
	if !errors.Is(e, ErrInvalidInput) {
		t.Fatal("missing invalid input classification")
	}
	var found *ValidationError
	if !errors.As(fmt.Errorf("wrapped: %w", e), &found) || found.Message() != "Please correct" {
		t.Fatal("missing wrapped details")
	}
	snapshot := e.Fields()
	snapshot["user.name"][0] = "changed"
	if e.Fields()["user.name"][0] != "required" {
		t.Fatal("details not copied")
	}
	state := NewFormState(url.Values{"user.name": {"one", "two"}, "password": {"secret"}, "_csrf": {"token"}}, []string{"user.name", "_csrf"}, e)
	if state.Value("user.name") != "one" || len(state.Values("user.name")) != 2 || state.Value("password") != "" || state.Value("_csrf") != "" {
		t.Fatal("allowlist not applied")
	}
	v := state.Values("user.name")
	v[0] = "changed"
	if state.Value("user.name") != "one" || len(state.Errors("user.name")) != 2 {
		t.Fatal("state is not independent")
	}
}

func TestConcurrentValidationStates(t *testing.T) {
	for i := range 32 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			value := fmt.Sprint(i)
			e := NewValidationError("form "+value, map[string][]string{"name": {value}})
			state := NewFormState(url.Values{"name": {value}}, []string{"name"}, e)
			if state.Value("name") != value || state.Errors("name")[0] != value || state.Message() != "form "+value {
				t.Fatal("request state leaked")
			}
		})
	}
}
