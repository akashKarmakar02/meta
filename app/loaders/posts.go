package loaders

import (
	"context"

	"github.com/akashKarmakar02/meta/routecontext"
)

// PostData is the demonstration loader result for the C03 SSR example. It is a
// plain value type so the page can read it back with an exact type through
// loaderdata.Get. It is a demonstration fixture, not real business data.
type PostData struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// LoadPost is a demonstration SSR loader bound to the posts/[postId] fixture
// route. It reads the postId path parameter through the B05 route snapshot and
// returns a typed struct. An unparseable postId is a loader error, which C03
// turns into a 500 before the page renders — this is the intended failure
// path for the example (the full status mapping is C05).
func LoadPost(_ context.Context, route routecontext.Route) (PostData, error) {
	id, err := route.Params.Int64("postId")
	if err != nil {
		return PostData{}, err
	}
	return PostData{
		ID:      id,
		Title:   "Example Post",
		Content: "Loaded on the server",
	}, nil
}
