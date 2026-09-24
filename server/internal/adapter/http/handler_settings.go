package http

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func (h *Handler) registerSettingsRoutes(app *fiber.App) {
	app.Get("/v1/settings", h.GetSettings)
	app.Put("/v1/settings", h.UpdateSettings)
	app.Put("/v1/settings/analiz-assignment", h.UpdateAnalizAssignment)
	app.Get("/v1/settings/github", h.GitHubStatus)
	app.Put("/v1/settings/github", h.SetGitHubToken)
	app.Delete("/v1/settings/github", h.DeleteGitHubToken)
	app.Get("/v1/settings/github/owners", h.GitHubOwners)
	app.Get("/v1/settings/github/repos", h.GitHubOwnerRepos)
}

func (h *Handler) GetSettings(c *fiber.Ctx) error {
	if h.settingsSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(errorResponse{
			Error: errorDetail{Message: "settings not enabled", Type: "service_unavailable"},
		})
	}
	out, err := h.settingsSvc.Get(h.enrichContext(c))
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(out)
}

// settingsRequestIsEmpty reports a request that names no field. Every field
// belongs here: one left out is a save the UI reports as done and the server
// refuses as empty.
func settingsRequestIsEmpty(req domain.UpdateSettingsRequest) bool {
	return req.WorkspaceRoot == "" && req.DefaultLanguage == "" &&
		req.PipelineContainerRuntime == "" && req.BoilerplateCatalogRepo == "" &&
		req.DefaultAssignee == ""
}

func (h *Handler) UpdateSettings(c *fiber.Ctx) error {
	if h.settingsSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(errorResponse{
			Error: errorDetail{Message: "settings not enabled", Type: "service_unavailable"},
		})
	}
	var req domain.UpdateSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if settingsRequestIsEmpty(req) {
		return badRequest(c, "at least one settings field is required")
	}
	out, err := h.settingsSvc.Update(h.enrichContext(c), req)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(out)
}

func (h *Handler) UpdateAnalizAssignment(c *fiber.Ctx) error {
	if h.settingsSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(errorResponse{
			Error: errorDetail{Message: "settings not enabled", Type: "service_unavailable"},
		})
	}
	var req domain.UpdateAnalizAssignmentRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	result, err := h.settingsSvc.UpdateAnalizAssignment(h.enrichContext(c), req)
	if err != nil {
		var missingErr *domain.MissingAnalizToolsError
		if errors.As(err, &missingErr) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(domain.AnalizAssignmentResult{
				Saved:        false,
				MissingTools: missingErr.Missing,
				Hint:         "Resend with confirm_grant_tools=true to grant the missing tools and save the assignment.",
			})
		}
		return internalError(c, err)
	}
	return c.JSON(result)
}
