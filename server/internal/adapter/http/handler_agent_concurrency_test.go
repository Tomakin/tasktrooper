package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/makifbaysal/tasktrooper/server/internal/application/agentconcurrency"
)

type fakeConcurrency struct{ st agentconcurrency.Status }

func (f *fakeConcurrency) Status() agentconcurrency.Status { return f.st }
func (f *fakeConcurrency) Set(_ context.Context, n int) (agentconcurrency.Status, error) {
	if n < 1 || n > 10 {
		return agentconcurrency.Status{}, fmt.Errorf("out of range")
	}
	f.st.Limit = n
	return f.st, nil
}

func TestAgentConcurrencyRoutes(t *testing.T) {
	f := &fakeConcurrency{st: agentconcurrency.Status{Limit: 1, Active: 1, Waiting: 2, Min: 1, Max: 10}}
	h := &Handler{agentConcurrency: f}
	app := fiber.New()
	h.registerAgentConcurrencyRoutes(app)

	do := func(method, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, "/v1/settings/agent-concurrency", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	if code, body := do("GET", ""); code != 200 || body["limit"] != float64(1) || body["waiting"] != float64(2) {
		t.Fatalf("get: %d %v", code, body)
	}
	if code, body := do("PUT", `{"limit":3}`); code != 200 || body["limit"] != float64(3) {
		t.Fatalf("put: %d %v", code, body)
	}
	for _, bad := range []string{`{"limit":0}`, `{"limit":11}`, `{}`, `not json`} {
		if code, _ := do("PUT", bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}
