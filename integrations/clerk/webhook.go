package metaclerk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	clerk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/gofiber/fiber/v3"
)

// WebhookHandlers are called for verified events. DeliveryID is stable across
// retries. Handlers must handle duplicates and events arriving out of order.
type WebhookHandlers struct {
	UserCreated func(context.Context, string, clerk.User) error
	UserUpdated func(context.Context, string, clerk.User) error
	UserDeleted func(context.Context, string, clerk.DeletedResource) error
}

// Webhook verifies and dispatches Clerk events. Mount it on a public POST API
// route, outside session policies. Unhandled event types receive a 204 response.
func (a *Integration) Webhook(h WebhookHandlers) (fiber.Handler, error) {
	if a.webhook == nil {
		return nil, errors.New("metaclerk: webhook signing secret is required")
	}
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		if c.Method() != fiber.MethodPost {
			return c.SendStatus(fiber.StatusMethodNotAllowed)
		}
		body := c.Body()
		if len(body) > 1<<20 {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		headers := make(http.Header)
		for _, name := range []string{"svix-id", "svix-timestamp", "svix-signature"} {
			headers.Set(name, c.Get(name))
		}
		if a.webhook.Verify(body, headers) != nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &event) != nil || event.Type == "" || len(event.Data) == 0 {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		id := c.Get("svix-id")
		var err error
		switch event.Type {
		case "user.created", "user.updated":
			var user clerk.User
			if json.Unmarshal(event.Data, &user) != nil || user.ID == "" {
				return c.SendStatus(fiber.StatusBadRequest)
			}
			if event.Type == "user.created" && h.UserCreated != nil {
				err = h.UserCreated(c.Context(), id, user)
			}
			if event.Type == "user.updated" && h.UserUpdated != nil {
				err = h.UserUpdated(c.Context(), id, user)
			}
		case "user.deleted":
			var user clerk.DeletedResource
			if json.Unmarshal(event.Data, &user) != nil || user.ID == "" {
				return c.SendStatus(fiber.StatusBadRequest)
			}
			if h.UserDeleted != nil {
				err = h.UserDeleted(c.Context(), id, user)
			}
		}
		if err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}, nil
}
