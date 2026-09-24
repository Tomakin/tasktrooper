package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
	"github.com/makifbaysal/tasktrooper/server/internal/port"
)

// The branch flow's GitHub calls (port.IntegrationGitHub). They hang off PRAPI
// so one SetBaseURL points every pull-request call at a test server.

func (a *PRAPI) FindOpenPullRequest(ctx context.Context, token, owner, repo, head, base string) (port.PullRequest, bool, error) {
	q := url.Values{}
	q.Set("head", owner+":"+head)
	q.Set("base", base)
	q.Set("state", "open")
	var out []prPayload
	if err := doJSONAt(ctx, a.baseURL, token, http.MethodGet, repoPath(owner, repo)+"/pulls?"+q.Encode(), nil, &out); err != nil {
		return port.PullRequest{}, false, err
	}
	if len(out) == 0 {
		return port.PullRequest{}, false, nil
	}
	return out[0].toPort(), true, nil
}

func (a *PRAPI) CreatePullRequestInto(ctx context.Context, token, owner, repo, head, base, title, body string) (port.PullRequest, error) {
	var out prPayload
	err := doJSONAt(ctx, a.baseURL, token, http.MethodPost, repoPath(owner, repo)+"/pulls", map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
		"draft": false,
	}, &out)
	if err != nil {
		return port.PullRequest{}, fmt.Errorf("open pull request %s → %s: %w", head, base, err)
	}
	return out.toPort(), nil
}

// MergeWithMergeCommit merges with a merge commit rather than a squash: the
// same branch goes on to the default branch later, and a squash here would give
// the integration branch a commit the default branch never gets, so the two
// histories would drift apart with every task.
func (a *PRAPI) MergeWithMergeCommit(ctx context.Context, token, owner, repo string, number int, expectedHeadSHA, title string) (string, error) {
	expectedHeadSHA = strings.TrimSpace(expectedHeadSHA)
	if expectedHeadSHA == "" {
		return "", fmt.Errorf("refusing to merge pull request #%d without the head commit it was checked at", number)
	}
	payload := map[string]any{
		"merge_method": "merge",
		"sha":          expectedHeadSHA,
	}
	if strings.TrimSpace(title) != "" {
		payload["commit_title"] = title
	}
	var out MergeResult
	if err := doJSONAt(ctx, a.baseURL, token, http.MethodPut, pullPath(owner, repo, number)+"/merge", payload, &out); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case http.StatusMethodNotAllowed:
				return "", fmt.Errorf("github refused to merge pull request #%d — it is not in a mergeable state: %s", number, apiErr.Message)
			case http.StatusConflict:
				return "", fmt.Errorf("github refused to merge pull request #%d — its head is no longer at %s: %s", number, shortSHA(expectedHeadSHA), apiErr.Message)
			}
		}
		return "", fmt.Errorf("merge pull request #%d: %w", number, err)
	}
	if !out.Merged || strings.TrimSpace(out.SHA) == "" {
		return "", fmt.Errorf("github reported pull request #%d as not merged: %s", number, out.Message)
	}
	return out.SHA, nil
}

func (a *PRAPI) ListPushRuns(ctx context.Context, token, owner, repo, branch, sha string) ([]port.ActionsRun, error) {
	q := url.Values{}
	q.Set("head_sha", sha)
	q.Set("event", "push")
	q.Set("branch", branch)
	q.Set("per_page", "100")
	var out struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	if err := doJSONAt(ctx, a.baseURL, token, http.MethodGet, repoPath(owner, repo)+"/actions/runs?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return mapActionsRuns(out.WorkflowRuns), nil
}

// RunFailureLog returns the tail of the first failing job's log. The card gets
// the error itself: an agent on this host cannot open a GitHub Actions page,
// so a link alone tells it only that something went wrong.
func (a *PRAPI) RunFailureLog(ctx context.Context, token, owner, repo string, runID int64, maxBytes int) (string, error) {
	jobs, err := listRunJobsAt(ctx, a.baseURL, token, owner, repo, runID)
	if err != nil {
		return "", err
	}
	for _, job := range jobs {
		if !strings.EqualFold(job.Conclusion, "failure") {
			continue
		}
		log, err := getJobLogsAt(ctx, a.baseURL, token, owner, repo, job.ID)
		if err != nil {
			return "", fmt.Errorf("read the log of job %q: %w", job.Name, err)
		}
		return job.Name + ":\n" + domain.TruncateTail(log, maxBytes), nil
	}
	return "", nil
}

var _ port.IntegrationGitHub = (*PRAPI)(nil)
