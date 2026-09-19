package http

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/makifbaysal/tasktrooper/server/internal/application/agentconcurrency"
)

// AgentConcurrencyControl is the concurrent-sessions setting as this layer
// uses it; declared here so the handler is testable without a limiter.
type AgentConcurrencyControl interface {
	Status() agentconcurrency.Status
	Set(ctx context.Context, n int) (agentconcurrency.Status, error)
}

func (h *Handler) registerAgentConcurrencyRoutes(app *fiber.App) {
	if h.agentConcurrency == nil {
		return
	}
	app.Get("/v1/settings/agent-concurrency", h.GetAgentConcurrency)
	app.Put("/v1/settings/agent-concurrency", h.PutAgentConcurrency)
}

func (h *Handler) GetAgentConcurrency(c *fiber.Ctx) error {
	return c.JSON(h.agentConcurrency.Status())
}

func (h *Handler) PutAgentConcurrency(c *fiber.Ctx) error {
	var req struct {
		Limit *int `json:"limit"`
	}
	if err := c.BodyParser(&req); err != nil || req.Limit == nil {
		return badRequest(c, "limit is required")
	}
	st, err := h.agentConcurrency.Set(h.enrichContext(c), *req.Limit)
	if err != nil {
		return badRequest(c, err.Error())
	}
	return c.JSON(st)
}
