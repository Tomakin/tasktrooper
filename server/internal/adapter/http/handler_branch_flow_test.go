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

	"github.com/makifbaysal/tasktrooper/server/internal/application/branchflow"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type fakeBranchFlow struct {
	flow       *domain.BranchFlow
	rec        *domain.TaskIntegration
	releaseErr error
}

func (f *fakeBranchFlow) Flow(context.Context, uuid.UUID) (domain.BranchFlow, error) {
	if f.flow == nil {
		return domain.BranchFlow{}, domain.ErrBranchFlowNotFound
	}
	return *f.flow, nil
}
func (f *fakeBranchFlow) SetFlow(_ context.Context, id uuid.UUID, branch string) (domain.BranchFlow, error) {
	if branch == "" {
		f.flow = nil
		return domain.BranchFlow{}, nil
	}
	if strings.Contains(branch, " ") {
		return domain.BranchFlow{}, domain.ErrReleaseNotReady
	}
	f.flow = &domain.BranchFlow{RepositoryID: id, IntegrationBranch: branch}
	return *f.flow, nil
}
func (f *fakeBranchFlow) TaskIntegration(context.Context, uuid.UUID, uuid.UUID) (domain.TaskIntegration, error) {
	if f.rec == nil {
		return domain.TaskIntegration{}, domain.ErrTaskIntegrationNotFound
	}
	return *f.rec, nil
}
func (f *fakeBranchFlow) Release(context.Context, uuid.UUID, uuid.UUID) (branchflow.ReleaseResult, error) {
	if f.flow == nil {
		return branchflow.ReleaseResult{}, domain.ErrBranchFlowNotFound
	}
	if f.releaseErr != nil {
		return branchflow.ReleaseResult{}, f.releaseErr
	}
	return branchflow.ReleaseResult{Task: domain.BoardTask{Column: domain.TaskColumnDone}}, nil
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
	if code, body := call(t, app, "PUT", base, `{"integration_branch":"development"}`); code != 200 ||
		body["enabled"] != true || body["integration_branch"] != "development" {
		t.Fatalf("on: %d %v", code, body)
	}
	if code, _ := call(t, app, "PUT", base, `{"integration_branch":"bad name"}`); code != 400 {
		t.Fatalf("invalid branch: %d", code)
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
	if code, _ := call(t, app, "POST", base+"/release", ""); code != 404 {
		t.Fatalf("release without a flow: %d", code)
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

	f.releaseErr = domain.ErrReleaseNotReady
	if code, body := call(t, app, "POST", base+"/release", ""); code != 409 {
		t.Fatalf("not ready: %d %v", code, body)
	}
	f.releaseErr = nil
	code, body := call(t, app, "POST", base+"/release", "")
	task, _ := body["task"].(map[string]any)
	if code != 200 || task["column"] != "done" {
		t.Fatalf("release: %d %v", code, body)
	}
}
