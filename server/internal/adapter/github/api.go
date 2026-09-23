// Package github, gh CLI yerine GitHub REST API ile konuşan ince bir istemci.
// Token kaynağı: OAuth App akışıyla alınan kullanıcı token'ı (bkz. oauth.go).
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiBase is a var (not const) so tests can point it at an httptest server;
// see secrets_test.go for the save/override/restore pattern.
var apiBase = "https://api.github.com"

var httpClient = &http.Client{Timeout: 15 * time.Second}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("github api: %d %s", e.Status, e.Message)
}

func doJSON(ctx context.Context, token, method, path string, body any, out any) error {
	return doJSONAt(ctx, "", token, method, path, body, out)
}

// resolveBase returns base if it is non-empty, else the package-default
// apiBase. This lets a caller (e.g. ActionsAPI) override the host per
// instance without disturbing doJSON's pre-existing global-var override
// convention (see secrets_test.go), which every other caller in this
// package still relies on.
func resolveBase(base string) string {
	if base == "" {
		return apiBase
	}
	return base
}

// doJSONAt is doJSON with an explicit base URL override. It exists so a few
// callers (the Actions client) can point requests at an httptest server via
// an instance field rather than the package-level apiBase var, without
// changing doJSON's signature or the behavior of its many existing callers.
func doJSONAt(ctx context.Context, base, token, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, resolveBase(base)+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &payload)
		if payload.Message == "" {
			payload.Message = strings.TrimSpace(string(data))
		}
		return &apiError{Status: resp.StatusCode, Message: payload.Message}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Identity is the account a token belongs to. The numeric ID matters as much as
// the login: GitHub links a commit to an account by the author email, and the
// only email guaranteed to resolve — no verified-email setup, no private-email
// leak — is the account's noreply address, which is built from both.
type Identity struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// NoReplyEmail is the address GitHub itself attributes commits with. Returns ""
// when the identity is incomplete, so callers fall back rather than write a
// malformed author into a commit that can never be rewritten.
func (i Identity) NoReplyEmail() string {
	if i.ID <= 0 || i.Login == "" {
		return ""
	}
	return fmt.Sprintf("%d+%s@users.noreply.github.com", i.ID, i.Login)
}

// UserIdentity, token sahibinin login + numeric id bilgisini döndürür.
func UserIdentity(ctx context.Context, token string) (Identity, error) {
	var out Identity
	if err := doJSON(ctx, token, http.MethodGet, "/user", nil, &out); err != nil {
		return Identity{}, err
	}
	return out, nil
}

// User, token'ın ait olduğu hesabın login adını döndürür (token doğrulama).
func User(ctx context.Context, token string) (string, error) {
	id, err := UserIdentity(ctx, token)
	if err != nil {
		return "", err
	}
	return id.Login, nil
}

type Repo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	// Owner is who the repo hangs off. It is carried because the owner list is
	// partly derived from it — see ListOwners.
	Owner struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "User" | "Organization"
	} `json:"owner"`
}

// CreateUserRepo, kullanıcının hesabında private repo oluşturur.
func CreateUserRepo(ctx context.Context, token, name string) (Repo, error) {
	var out Repo
	err := doJSON(ctx, token, http.MethodPost, "/user/repos", map[string]any{
		"name":    name,
		"private": true,
	}, &out)
	return out, err
}

// CreateRepoIn, verilen owner altında private repo oluşturur. Owner token
// sahibinin login'i ise kullanıcı hesabında, değilse org altında açılır.
func CreateRepoIn(ctx context.Context, token, owner, name string) (Repo, error) {
	login, err := User(ctx, token)
	if err != nil {
		return Repo{}, err
	}
	if owner == "" || strings.EqualFold(owner, login) {
		return CreateUserRepo(ctx, token, name)
	}
	var out Repo
	err = doJSON(ctx, token, http.MethodPost, "/orgs/"+owner+"/repos", map[string]any{
		"name":    name,
		"private": true,
	}, &out)
	return out, err
}

type Owner struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "user" | "org"
}

// ListOwners, token sahibini ve üyesi olduğu org'ları döndürür.
//
// İki kaynaktan toplanır, çünkü tek başına hiçbiri yeterli değil. `/user/orgs`
// klasik token'da `read:org` scope'u yoksa, fine-grained token'da org'a ayrıca
// yetki verilmemişse sessizce boş döner — kullanıcı org'un üyesi olduğu halde
// listede göremez, ki bu tam da bildirilen hata. Org üyeliğiyle görünen
// repoların sahipleri ise o scope'a bağlı değil; erişilebilen bir org repo,
// import edilebilir bir org'un kanıtıdır. Birleşimini vermek, listeyi eksik
// göstermekten her koşulda daha doğru.
func ListOwners(ctx context.Context, token string) ([]Owner, error) {
	login, err := User(ctx, token)
	if err != nil {
		return nil, err
	}
	owners := []Owner{{Login: login, Type: "user"}}
	seen := map[string]bool{strings.ToLower(login): true}
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		owners = append(owners, Owner{Login: strings.TrimSpace(name), Type: "org"})
	}

	var orgs []struct {
		Login string `json:"login"`
	}
	// Bu çağrının hatası ölümcül değil: token'ın org okuma yetkisi yoksa 403
	// döner ve o durumda aşağıdaki repo taraması hâlâ iş görür. Hata yüzünden
	// tüm listeyi düşürmek, kullanıcıya kendi hesabını bile göstermemek olur.
	orgErr := doJSON(ctx, token, http.MethodGet, "/user/orgs?per_page=100", nil, &orgs)
	if orgErr == nil {
		for _, o := range orgs {
			add(o.Login)
		}
	}

	var memberRepos []Repo
	repoErr := doJSON(ctx, token, http.MethodGet,
		"/user/repos?per_page=100&sort=pushed&affiliation=organization_member", nil, &memberRepos)
	if repoErr == nil {
		for _, r := range memberRepos {
			if !strings.EqualFold(r.Owner.Login, login) {
				add(r.Owner.Login)
			}
		}
	}

	if orgErr != nil && repoErr != nil {
		return nil, orgErr
	}
	return owners, nil
}

// ListOwnerRepos, owner altındaki repoları döndürür (en yeni güncellenen önce).
func ListOwnerRepos(ctx context.Context, token, owner string) ([]Repo, error) {
	login, err := User(ctx, token)
	if err != nil {
		return nil, err
	}
	if owner == "" || strings.EqualFold(owner, login) {
		var out []Repo
		err := doJSON(ctx, token, http.MethodGet, "/user/repos?per_page=100&sort=pushed&affiliation=owner", nil, &out)
		return out, err
	}
	var out []Repo
	orgErr := doJSON(ctx, token, http.MethodGet, "/orgs/"+owner+"/repos?per_page=100&sort=pushed", nil, &out)
	if orgErr == nil && len(out) > 0 {
		return out, nil
	}
	// Org endpoint'i token'ın org üzerinde okuma yetkisi olmasını ister; sadece
	// belirli repolara yetki verilmiş bir token orada 403 alır ya da boş liste
	// görür, oysa o repoları kendi listesinden görebiliyordur. Aynı sebeple
	// ListOwners org'u zaten bu yoldan bulmuş olabilir — orada bulup burada
	// gösterememek, açılmayan bir seçenek sunmak olurdu.
	var member []Repo
	if err := doJSON(ctx, token, http.MethodGet,
		"/user/repos?per_page=100&sort=pushed&affiliation=organization_member", nil, &member); err != nil {
		if orgErr != nil {
			return nil, orgErr
		}
		return out, nil
	}
	filtered := make([]Repo, 0, len(member))
	for _, r := range member {
		if strings.EqualFold(r.Owner.Login, owner) {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 && orgErr != nil {
		return nil, orgErr
	}
	return filtered, nil
}

// GetRepo, owner/name reposunu döndürür (default branch için).
func GetRepo(ctx context.Context, token, owner, name string) (Repo, error) {
	var out Repo
	err := doJSON(ctx, token, http.MethodGet, "/repos/"+owner+"/"+name, nil, &out)
	return out, err
}

// FindOpenPR, head branch'i için açık PR varsa URL'ini döndürür; yoksa "".
//
// base narrows the search to pull requests going into that branch. It matters
// on a repository running the two-stage flow, where the same branch has a
// second open pull request into the integration branch: without the filter
// this could hand back the integration PR as if it were the task's own.
func FindOpenPR(ctx context.Context, token, owner, name, headOwner, branch, base string) (string, error) {
	q := url.Values{}
	q.Set("head", headOwner+":"+branch)
	q.Set("state", "open")
	if strings.TrimSpace(base) != "" {
		q.Set("base", base)
	}
	var out []struct {
		HTMLURL string `json:"html_url"`
	}
	if err := doJSON(ctx, token, http.MethodGet, "/repos/"+owner+"/"+name+"/pulls?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0].HTMLURL, nil
}

// CreatePullRequest opens the task's pull request READY FOR REVIEW and returns
// its URL.
//
// It used to open a draft (it was called CreateDraftPR), which was wrong in both
// directions. A draft is a statement that the change is not finished, while this
// PR is opened at the moment the board hands the work to review — and nothing in
// the system ever took it out of draft, because the REST API cannot (see
// MarkPullRequestReady). GitHub refuses to merge a draft, so every task PR was
// unmergeable by construction: the board could carry a task to `done` and the
// change still sat on a branch. Opening ready is what makes the merge at `done`
// possible at all.
func CreatePullRequest(ctx context.Context, token, owner, name, head, base, title string) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	err := doJSON(ctx, token, http.MethodPost, repoPath(owner, name)+"/pulls", map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"draft": false,
	}, &out)
	if err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// MarkPullRequestReady takes a pull request out of draft state.
//
// It uses GraphQL, and it has to. GitHub's REST "Update a pull request"
// (PATCH /repos/{owner}/{repo}/pulls/{n}) documents exactly five body
// parameters — title, body, state, base, maintainer_can_modify — and `draft` is
// not one of them; it only exists on the CREATE call. Sending draft:false there
// changes nothing and reports success, which is the worst possible failure for
// this operation: the merge that follows would hit "Draft pull requests cannot
// be merged" with nothing having gone wrong anywhere upstream. The GraphQL
// mutation markPullRequestReadyForReview is the only API that flips the bit,
// which is also why `gh pr ready` is implemented against it.
//
// The mutation is keyed by the PR's GraphQL node id, so this reads the PR over
// REST first. Two round-trips for a step that is now rare (task PRs open ready
// for review — see CreatePullRequest) is the right trade against caching a node
// id nothing else needs.
//
// It survives as a REPAIR PATH. Every task PR opened before that change is a
// draft on GitHub right now, and those tasks still have to be mergeable when
// they reach done; deleting this would strand them with no way forward but a
// human clicking "Ready for review". A merge that finds draft:false never calls
// it.
func MarkPullRequestReady(ctx context.Context, token, owner, repo string, number int) error {
	var pr struct {
		NodeID string `json:"node_id"`
		Draft  bool   `json:"draft"`
	}
	if err := doJSON(ctx, token, http.MethodGet, pullPath(owner, repo, number), nil, &pr); err != nil {
		return fmt.Errorf("read pull request #%d before marking it ready: %w", number, err)
	}
	if !pr.Draft {
		return nil
	}
	if pr.NodeID == "" {
		return fmt.Errorf("pull request #%d reports no node id, so it cannot be marked ready for review", number)
	}
	body := map[string]any{
		"query": "mutation($id:ID!){markPullRequestReadyForReview(input:{pullRequestId:$id}){pullRequest{number isDraft}}}",
		"variables": map[string]any{
			"id": pr.NodeID,
		},
	}
	// GraphQL answers 200 with an `errors` array for a failed mutation, so the
	// transport-level check doJSON does is not enough on this endpoint.
	var out struct {
		Data struct {
			MarkPullRequestReadyForReview struct {
				PullRequest struct {
					Number  int  `json:"number"`
					IsDraft bool `json:"isDraft"`
				} `json:"pullRequest"`
			} `json:"markPullRequestReadyForReview"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doJSON(ctx, token, http.MethodPost, "/graphql", body, &out); err != nil {
		return fmt.Errorf("mark pull request #%d ready for review: %w", number, err)
	}
	if len(out.Errors) > 0 {
		messages := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			messages = append(messages, e.Message)
		}
		return fmt.Errorf("mark pull request #%d ready for review: %s", number, strings.Join(messages, "; "))
	}
	if out.Data.MarkPullRequestReadyForReview.PullRequest.IsDraft {
		return fmt.Errorf("pull request #%d is still a draft after markPullRequestReadyForReview", number)
	}
	return nil
}

// MergeResult is what GitHub reports about a completed merge.
type MergeResult struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

// MergePullRequest squash-merges a pull request and returns the commit it
// created.
//
// expectedHeadSHA is required and is sent as the `sha` precondition. GitHub
// merges whatever the head branch points at WHEN THE REQUEST LANDS, so a merge
// without it is a merge of unknown code: the gate that checked the PR read it
// seconds earlier, and a push in between (an agent run finishing, a human
// pushing a "small fix") would be merged unseen. With it, that race is a 409
// from GitHub instead — the same failure direction as every other gate here.
// Callers pass the SHA they verified, never one re-read just before the call,
// or the precondition would only certify that no push happened during the last
// millisecond.
//
// The merge method is squash and is not configurable. A task branch is a stream
// of agent commits ("wip:", verify/fix rounds, review answers) whose individual
// history is noise on the default branch, and one commit per task is what makes
// the merge commit usable as the deploy target the follow-up watch needs.
func MergePullRequest(ctx context.Context, token, owner, repo string, number int, expectedHeadSHA, commitTitle, commitBody string) (MergeResult, error) {
	expectedHeadSHA = strings.TrimSpace(expectedHeadSHA)
	if expectedHeadSHA == "" {
		return MergeResult{}, fmt.Errorf("refusing to merge pull request #%d without the head commit it was verified at", number)
	}
	payload := map[string]any{
		"merge_method": "squash",
		"sha":          expectedHeadSHA,
	}
	if strings.TrimSpace(commitTitle) != "" {
		payload["commit_title"] = commitTitle
	}
	if strings.TrimSpace(commitBody) != "" {
		payload["commit_message"] = commitBody
	}
	var out MergeResult
	if err := doJSON(ctx, token, http.MethodPut, pullPath(owner, repo, number)+"/merge", payload, &out); err != nil {
		// 405 and 409 are the two GitHub answers a caller must be able to tell
		// apart from "the API is down": 405 is "this PR is not in a mergeable
		// state" (draft, conflict, required check red, protected branch), 409 is
		// "head has moved since the SHA you passed". Both are named here rather
		// than left as a bare status code, because the agent reading the tool
		// result decides what to do next from this sentence.
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case http.StatusMethodNotAllowed:
				return MergeResult{}, fmt.Errorf("github refused to merge pull request #%d — it is not in a mergeable state (draft, conflicting, a required check not green, or branch protection): %s", number, apiErr.Message)
			case http.StatusConflict:
				return MergeResult{}, fmt.Errorf("github refused to merge pull request #%d — its head is no longer at %s, so something was pushed after this task was verified: %s", number, shortSHA(expectedHeadSHA), apiErr.Message)
			}
		}
		return MergeResult{}, fmt.Errorf("merge pull request #%d: %w", number, err)
	}
	if !out.Merged || strings.TrimSpace(out.SHA) == "" {
		return out, fmt.Errorf("github reported pull request #%d as not merged: %s", number, out.Message)
	}
	return out, nil
}

// DeleteBranch removes a branch from the repository (DELETE on its ref).
//
// Used after a successful squash merge, where the branch's content is already on
// the default branch and leaving it behind gives the next run a branch that
// looks alive. A failure is the caller's to report, never to retry into: the
// merge it follows has already happened.
func DeleteBranch(ctx context.Context, token, owner, repo, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("no branch name to delete")
	}
	path := repoPath(owner, repo) + "/git/refs/heads/" + refSegments(branch)
	if err := doJSON(ctx, token, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	return nil
}

// shortSHA renders a commit the way a human reads one in a git log. Mirrors
// domain.ShortSHA; kept local so this adapter package keeps depending on
// nothing but the standard library.
func shortSHA(sha string) string {
	if len(sha) < 12 {
		return sha
	}
	return sha[:12]
}

// ParseOwnerRepo, origin URL'inden owner ve repo adını çıkarır.
// Desteklenen biçimler: https://github.com/o/r(.git), git@github.com:o/r(.git).
func ParseOwnerRepo(origin string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(origin)
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	case strings.Contains(s, "github.com/"):
		idx := strings.Index(s, "github.com/")
		s = s[idx+len("github.com/"):]
	default:
		return "", "", false
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
