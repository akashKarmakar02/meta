package routing

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// Fiber remains the route matcher; this exercises multiple captures through
// its actual HTTP dispatch rather than a second, test-only pattern matcher.
func TestFiberNestedParameterCapture(t *testing.T) {
	app := fiber.New()
	app.Get("/organizations/:orgId/teams/:teamId/members/:memberId", func(c fiber.Ctx) error {
		return c.JSON(map[string]string{
			"orgId": c.Params("orgId"), "teamId": c.Params("teamId"), "memberId": c.Params("memberId"),
		})
	})
	for _, tc := range []struct {
		name, url string
		status    int
		params    map[string]string
	}{
		{"three captures", "/organizations/acme/teams/engineering/members/alice?teamId=wrong", 200, map[string]string{"orgId": "acme", "teamId": "engineering", "memberId": "alice"}},
		{"different request", "/organizations/other/teams/sales/members/bob", 200, map[string]string{"orgId": "other", "teamId": "sales", "memberId": "bob"}},
		{"missing", "/organizations/acme/teams/engineering/members", 404, nil},
		{"extra", "/organizations/acme/teams/engineering/members/alice/extra", 404, nil},
		{"wrong static", "/organizations/acme/groups/engineering/members/alice", 404, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.params == nil {
				return
			}
			var got map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.params) {
				t.Fatalf("params = %#v, want %#v", got, tc.params)
			}
		})
	}
}
