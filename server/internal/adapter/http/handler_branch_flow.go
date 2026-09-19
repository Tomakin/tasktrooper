package http

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/application/branchflow"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

// BranchFlowControl is the two-stage delivery as this layer uses it; declared
// here so the handler is testable without GitHub or a board.
type BranchFlowControl interface {
	Flow(ctx context.Context, repositoryID uuid.UUID) (domain.BranchFlow, error)
	SetFlow(ctx context.Context, repositoryID uuid.UUID, integrationBranch string) (domain.BranchFlow, error)
	TaskIntegration(ctx context.Context, repositoryID, taskID uuid.UUID) (domain.TaskIntegration, error)
	Release(ctx context.Context, repositoryID, taskID uuid.UUID) (branchflow.ReleaseResult, error)
}

func (h *Handler) registerBranchFlowRoutes(app fiber.Router) {
	if h.branchFlow == nil {
		return
	}
	app.Get("/v1/repositories/:id/branch-flow", h.GetBranchFlow)
	app.Put("/v1/repositories/:id/branch-flow", h.PutBranchFlow)
	app.Get("/v1/repositories/:id/tasks/:taskId/integration", h.GetTaskIntegration)
	app.Post("/v1/repositories/:id/tasks/:taskId/release", h.ReleaseTask)
}

type branchFlowResponse struct {
	Enabled           bool   `json:"enabled"`
	IntegrationBranch string `json:"integration_branch"`
}

func (h *Handler) branchFlowResponse(c *fiber.Ctx, repositoryID uuid.UUID) error {
	flow, err := h.branchFlow.Flow(h.enrichContext(c), repositoryID)
	if errors.Is(err, domain.ErrBranchFlowNotFound) {
		return c.JSON(branchFlowResponse{})
	}
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(branchFlowResponse{Enabled: true, IntegrationBranch: flow.IntegrationBranch})
}

func (h *Handler) GetBranchFlow(c *fiber.Ctx) error {
	repositoryID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid repository id")
	}
	return h.branchFlowResponse(c, repositoryID)
}

func (h *Handler) PutBranchFlow(c *fiber.Ctx) error {
	repositoryID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, "invalid repository id")
	}
	var req struct {
		IntegrationBranch string `json:"integration_branch"`
	}
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	// An unknown repository is refused by the table's foreign key.
	if _, err := h.branchFlow.SetFlow(h.enrichContext(c), repositoryID, req.IntegrationBranch); err != nil {
		return badRequest(c, err.Error())
	}
	return h.branchFlowResponse(c, repositoryID)
}

func (h *Handler) GetTaskIntegration(c *fiber.Ctx) error {
	repositoryID, taskID, err := parseRepositoryTaskParams(c)
	if err != nil {
		return badRequest(c, "invalid repository or task id")
	}
	ctx := h.enrichContext(c)
	flow, err := h.branchFlow.Flow(ctx, repositoryID)
	if errors.Is(err, domain.ErrBranchFlowNotFound) {
		return c.JSON(fiber.Map{"enabled": false})
	}
	if err != nil {
		return internalError(c, err)
	}
	rec, err := h.branchFlow.TaskIntegration(ctx, repositoryID, taskID)
	if errors.Is(err, domain.ErrTaskIntegrationNotFound) {
		return c.JSON(fiber.Map{"enabled": true, "integration_branch": flow.IntegrationBranch})
	}
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(fiber.Map{"enabled": true, "integration_branch": flow.IntegrationBranch, "integration": rec})
}

func (h *Handler) ReleaseTask(c *fiber.Ctx) error {
	repositoryID, taskID, err := parseRepositoryTaskParams(c)
	if err != nil {
		return badRequest(c, "invalid repository or task id")
	}
	res, err := h.branchFlow.Release(h.enrichContext(c), repositoryID, taskID)
	switch {
	case errors.Is(err, domain.ErrBranchFlowNotFound):
		return notFound(c, err.Error())
	case errors.Is(err, domain.ErrReleaseNotReady):
		return c.Status(fiber.StatusConflict).JSON(errorResponse{
			Error: errorDetail{Message: err.Error(), Type: "release_not_ready"},
		})
	case err != nil:
		return badRequestErr(c, err)
	}
	return c.JSON(res)
}
