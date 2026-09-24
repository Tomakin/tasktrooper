package board

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

// A reviewer that can say who should own a card but cannot make it so leaves
// the card where it was with a comment on it — which is exactly what happened
// twice on a board whose todo column had no owner.
type assigneeRecordingTasks struct {
	*fakeTaskManager
	updated domain.UpdateBoardTaskRequest
}

func (m *assigneeRecordingTasks) UpdateTask(_ context.Context, _, _ uuid.UUID, req domain.UpdateBoardTaskRequest) (domain.BoardTask, error) {
	m.updated = req
	return domain.BoardTask{ID: uuid.New()}, nil
}

type fakeTeam struct{ agents []domain.Agent }

func (f fakeTeam) ListAgents(context.Context) ([]domain.Agent, error) { return f.agents, nil }

func updateWith(t *testing.T, args map[string]any, agents []domain.Agent) (*assigneeRecordingTasks, domain.ToolResult) {
	t.Helper()
	tasks := &assigneeRecordingTasks{fakeTaskManager: &fakeTaskManager{}}
	tool := newUpdateTaskTool(&ToolKit{Tasks: tasks, Team: fakeTeam{agents: agents}})
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tasks, tool.Execute(context.Background(), string(raw))
}

func TestUpdateTaskReassignsByName(t *testing.T) {
	frontend := domain.Agent{ID: uuid.New(), Name: "frontend-developer"}
	taskID := uuid.New()

	tasks, res := updateWith(t, map[string]any{"task_id": taskID.String(), "assignee": "frontend-developer"},
		[]domain.Agent{frontend, {ID: uuid.New(), Name: "system-architect"}})

	if res.IsError {
		t.Fatalf("reassign failed: %s", res.Content)
	}
	if !tasks.updated.AssigneeAgentID.Present || tasks.updated.AssigneeAgentID.Value == nil {
		t.Fatalf("no assignee sent: %+v", tasks.updated.AssigneeAgentID)
	}
	if *tasks.updated.AssigneeAgentID.Value != frontend.ID {
		t.Errorf("assigned %s, want %s", tasks.updated.AssigneeAgentID.Value, frontend.ID)
	}
}

func TestUpdateTaskClearsAndKeepsTheAssignee(t *testing.T) {
	taskID := uuid.New()

	cleared, res := updateWith(t, map[string]any{"task_id": taskID.String(), "assignee": "-"}, nil)
	if res.IsError {
		t.Fatalf("clearing failed: %s", res.Content)
	}
	if !cleared.updated.AssigneeAgentID.Present || cleared.updated.AssigneeAgentID.Value != nil {
		t.Errorf(`"-" must send an explicit null: %+v`, cleared.updated.AssigneeAgentID)
	}

	untouched, res := updateWith(t, map[string]any{"task_id": taskID.String(), "priority": "high"}, nil)
	if res.IsError {
		t.Fatalf("update failed: %s", res.Content)
	}
	if untouched.updated.AssigneeAgentID.Present {
		t.Errorf("an omitted assignee must leave the current one alone: %+v", untouched.updated.AssigneeAgentID)
	}
}

func TestUpdateTaskRefusesAnUnknownAssignee(t *testing.T) {
	_, res := updateWith(t, map[string]any{"task_id": uuid.New().String(), "assignee": "nobody"},
		[]domain.Agent{{ID: uuid.New(), Name: "frontend-developer"}})
	if !res.IsError || !strings.Contains(res.Content, "frontend-developer") {
		t.Fatalf("the refusal must name the valid agents: %+v", res)
	}
}
