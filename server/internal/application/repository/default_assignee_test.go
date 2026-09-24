package repository

// The default assignee: a board whose columns are owned by reviewers has no
// owner for todo, so a task created without one used to sit there with no run
// and no error. This is the setting that gives it one.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func TestCreateTaskWithoutAnAssigneeUsesTheDefault(t *testing.T) {
	frontendID := uuid.New()
	f := newAnalizAssignmentFixture(
		domain.AppSettings{DefaultAssignee: domain.AgentFrontendDeveloper},
		[]domain.Agent{
			{ID: uuid.New(), Name: domain.AgentSystemArchitect},
			{ID: frontendID, Name: domain.AgentFrontendDeveloper},
		},
	)

	task := f.create(t, domain.CreateBoardTaskRequest{TaskType: domain.TaskTypeTask})

	require.NotNil(t, task.AssigneeAgentID, "a task dropped in todo must have an owner")
	assert.Equal(t, frontendID, *task.AssigneeAgentID)
}

func TestAnExplicitAssigneeWins(t *testing.T) {
	frontendID := uuid.New()
	backendID := uuid.New()
	f := newAnalizAssignmentFixture(
		domain.AppSettings{DefaultAssignee: domain.AgentFrontendDeveloper},
		[]domain.Agent{
			{ID: frontendID, Name: domain.AgentFrontendDeveloper},
			{ID: backendID, Name: domain.AgentBackendDeveloper},
		},
	)

	task := f.create(t, domain.CreateBoardTaskRequest{TaskType: domain.TaskTypeTask, AssigneeAgentID: &backendID})

	require.NotNil(t, task.AssigneeAgentID)
	assert.Equal(t, backendID, *task.AssigneeAgentID, "the default only fills an empty assignee")
}

func TestNoDefaultLeavesTheTaskUnassigned(t *testing.T) {
	for name, settings := range map[string]domain.AppSettings{
		"unset":         {},
		"unknown agent": {DefaultAssignee: "nobody-by-that-name"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAnalizAssignmentFixture(settings, []domain.Agent{{ID: uuid.New(), Name: domain.AgentFrontendDeveloper}})
			task := f.create(t, domain.CreateBoardTaskRequest{TaskType: domain.TaskTypeTask})
			assert.Nil(t, task.AssigneeAgentID)
		})
	}
}

func TestAnalizAssignmentStillWins(t *testing.T) {
	architectID := uuid.New()
	frontendID := uuid.New()
	f := newAnalizAssignmentFixture(
		domain.AppSettings{DefaultAssignee: domain.AgentFrontendDeveloper, AnalizAssigneeBackend: domain.AgentSystemArchitect},
		[]domain.Agent{
			{ID: architectID, Name: domain.AgentSystemArchitect},
			{ID: frontendID, Name: domain.AgentFrontendDeveloper},
		},
	)

	task := f.create(t, domain.CreateBoardTaskRequest{TaskType: domain.TaskTypeAnaliz})

	require.NotNil(t, task.AssigneeAgentID)
	assert.Equal(t, architectID, *task.AssigneeAgentID, "an analiz task follows its own setting")
}

func TestResolveDefaultAssigneeWithoutWiring(t *testing.T) {
	f := newAssigneeFixture()
	assert.Nil(t, f.svc.resolveDefaultAssignee(context.Background()))
}
