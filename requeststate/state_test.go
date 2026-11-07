package requeststate

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestKeysAreDistinct(t *testing.T) {
	a, b := NewKey[string](), NewKey[string]()
	ctx := context.WithValue(context.Background(), a, "alice")
	if v, ok := Get(ctx, a); !ok || v != "alice" {
		t.Fatalf("got %q, %v", v, ok)
	}
	if _, ok := Get(ctx, b); ok {
		t.Fatal("another key retrieved the value")
	}
}

func TestConcurrentContextIsolation(t *testing.T) {
	key := NewKey[string]()
	app := fiber.New()
	app.Get("/:user", func(c fiber.Ctx) error {
		Set(c, key, c.Params("user"))
		v, _ := Get(c.Context(), key)
		return c.SendString(v)
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("user%d", i)
			res, err := app.Test(httptest.NewRequest("GET", "/"+want, nil))
			if err != nil {
				t.Error(err)
				return
			}
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil || string(body) != want {
				t.Errorf("got %q, %v; want %q", body, err, want)
			}
		}(i)
	}
	wg.Wait()
}
