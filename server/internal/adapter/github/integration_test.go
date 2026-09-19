package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationPullRequestCalls(t *testing.T) {
	var mergeBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasSuffix(got, "tok") {
			t.Errorf("auth header %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls":
			q := r.URL.Query()
			if q.Get("head") != "acme:feature/a-1" || q.Get("base") != "development" || q.Get("state") != "open" {
				t.Errorf("find query %v", q)
			}
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["base"] != "development" || body["head"] != "feature/a-1" || body["draft"] != false {
				t.Errorf("create body %v", body)
			}
			_, _ = io.WriteString(w, `{"number":7,"html_url":"https://github.com/acme/app/pull/7","state":"open","mergeable_state":"unknown","head":{"ref":"feature/a-1","sha":"abc"},"base":{"ref":"development"}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/app/pulls/7/merge":
			_ = json.NewDecoder(r.Body).Decode(&mergeBody)
			_, _ = io.WriteString(w, `{"sha":"m3rg3","merged":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/actions/runs":
			q := r.URL.Query()
			if q.Get("head_sha") != "m3rg3" || q.Get("event") != "push" || q.Get("branch") != "development" {
				t.Errorf("runs query %v", q)
			}
			_, _ = io.WriteString(w, `{"workflow_runs":[{"id":1,"status":"completed","conclusion":"success","event":"push","head_branch":"development","html_url":"https://github.com/acme/app/actions/runs/1"}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	api := NewPRAPI()
	api.SetBaseURL(srv.URL)
	ctx := context.Background()

	if _, ok, err := api.FindOpenPullRequest(ctx, "tok", "acme", "app", "feature/a-1", "development"); err != nil || ok {
		t.Fatalf("find: ok=%v err=%v", ok, err)
	}
	pr, err := api.CreatePullRequestInto(ctx, "tok", "acme", "app", "feature/a-1", "development", "t", "b")
	if err != nil || pr.Number != 7 || pr.BaseRef != "development" || pr.HeadSHA != "abc" {
		t.Fatalf("create: %+v %v", pr, err)
	}
	sha, err := api.MergeWithMergeCommit(ctx, "tok", "acme", "app", 7, "abc", "Merge A-1 into development")
	if err != nil || sha != "m3rg3" {
		t.Fatalf("merge: %q %v", sha, err)
	}
	if mergeBody["merge_method"] != "merge" || mergeBody["sha"] != "abc" {
		t.Errorf("merge body %v", mergeBody)
	}
	runs, err := api.ListPushRuns(ctx, "tok", "acme", "app", "development", "m3rg3")
	if err != nil || len(runs) != 1 || runs[0].Conclusion != "success" {
		t.Fatalf("runs: %+v %v", runs, err)
	}
}

func TestMergeWithMergeCommitNamesTheRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `{"message":"Pull Request is not mergeable"}`)
	}))
	defer srv.Close()
	api := NewPRAPI()
	api.SetBaseURL(srv.URL)
	_, err := api.MergeWithMergeCommit(context.Background(), "tok", "acme", "app", 7, "abc", "")
	if err == nil || !strings.Contains(err.Error(), "not in a mergeable state") {
		t.Fatalf("err = %v", err)
	}
	if _, err := api.MergeWithMergeCommit(context.Background(), "tok", "acme", "app", 7, " ", ""); err == nil {
		t.Fatal("a merge without the expected head must be refused")
	}
}
