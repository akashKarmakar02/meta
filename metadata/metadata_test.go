package metadata_test

import (
	"context"
	"testing"

	"github.com/akashKarmakar02/meta/internal/metastate"
	"github.com/akashKarmakar02/meta/metadata"
)

func TestRequestMetadataIsolation(t *testing.T) {
	a := metastate.New(context.Background())
	b := metastate.New(context.Background())
	metadata.Set(a, metadata.Page{Title: "A"})
	metadata.Set(b, metadata.Page{Title: "B"})
	if got := metastate.Get(a).Title; got != "A" {
		t.Fatalf("first request: %q", got)
	}
	if got := metastate.Get(b).Title; got != "B" {
		t.Fatalf("second request: %q", got)
	}
}
