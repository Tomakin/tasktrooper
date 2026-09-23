package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type fakeBranchFlow struct {
	flow *domain.BranchFlow
	rec  *domain.TaskIntegration
}

func (f *fakeBranchFlow) Flow(context.Context, uuid.UUID) (domain.BranchFlow, error) {
	if f.flow == nil {
		return domain.BranchFlow{}, domain.ErrBranchFlowNotFound
	}
	return *f.flow, nil
}
func (f *fakeBranchFlow) SetFlow(_ context.Context, id uuid.UUID, branch, release string) (domain.BranchFlow, error) {
	if branch == "" {
		f.flow = nil
		return domain.BranchFlow{}, nil
	}
	if strings.Contains(branch, " ") || strings.Contains(release, " ") {
		return domain.BranchFlow{}, domain.ErrReleaseNotReady
	}
	f.flow = &domain.BranchFlow{RepositoryID: id, IntegrationBranch: branch, ReleaseBranch: release}
	return *f.flow, nil
}
func (f *fakeBranchFlow) TaskIntegration(context.Context, uuid.UUID, uuid.UUID) (domain.TaskIntegration, error) {
	if f.rec == nil {
		return domain.TaskIntegration{}, domain.ErrTaskIntegrationNotFound
	}
	return *f.rec, nil
}

func branchFlowApp(f *fakeBranchFlow) *fiber.App {
	h := &Handler{branchFlow: f}
	app := fiber.New()
	h.registerBranchFlowRoutes(app)
	return app
}

func call(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

func TestBranchFlowSettingsRoundTrip(t *testing.T) {
	f := &fakeBranchFlow{}
	app := branchFlowApp(f)
	base := "/v1/repositories/" + uuid.NewString() + "/branch-flow"

	if code, body := call(t, app, "GET", base, ""); code != 200 || body["enabled"] != false {
		t.Fatalf("off: %d %v", code, body)
	}
	if code, body := call(t, app, "PUT", base, `{"integration_branch":"development","release_branch":"master"}`); code != 200 ||
		body["enabled"] != true || body["integration_branch"] != "development" || body["release_branch"] != "master" {
		t.Fatalf("on: %d %v", code, body)
	}
	if code, _ := call(t, app, "PUT", base, `{"integration_branch":"bad name"}`); code != 400 {
		t.Fatalf("invalid branch: %d", code)
	}
	if code, _ := call(t, app, "PUT", base, `{"integration_branch":"development","release_branch":"bad name"}`); code != 400 {
		t.Fatalf("invalid release branch: %d", code)
	}
	if code, body := call(t, app, "PUT", base, `{"integration_branch":""}`); code != 200 || body["enabled"] != false {
		t.Fatalf("off again: %d %v", code, body)
	}
	if code, _ := call(t, app, "GET", "/v1/repositories/nope/branch-flow", ""); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
}

func TestTaskIntegrationAndRelease(t *testing.T) {
	f := &fakeBranchFlow{}
	app := branchFlowApp(f)
	base := "/v1/repositories/" + uuid.NewString() + "/tasks/" + uuid.NewString()

	if _, body := call(t, app, "GET", base+"/integration", ""); body["enabled"] != false {
		t.Fatalf("no flow: %v", body)
	}

	f.flow = &domain.BranchFlow{IntegrationBranch: "development"}
	if _, body := call(t, app, "GET", base+"/integration", ""); body["enabled"] != true || body["integration"] != nil {
		t.Fatalf("flow, no record yet: %v", body)
	}
	f.rec = &domain.TaskIntegration{Status: domain.IntegrationMerged, DeployStatus: domain.IntegrationDeployPending}
	_, body := call(t, app, "GET", base+"/integration", "")
	rec, _ := body["integration"].(map[string]any)
	if rec["status"] != "merged" || rec["deploy_status"] != "pending" {
		t.Fatalf("record: %v", body)
	}

	if code, _ := call(t, app, "POST", base+"/release", ""); code != 404 {
		t.Fatalf("the release endpoint is gone: %d", code)
	}
}
