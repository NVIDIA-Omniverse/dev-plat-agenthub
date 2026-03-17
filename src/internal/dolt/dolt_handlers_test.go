package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// UpdateAliveByName
// --------------------------------------------------------------------------

func TestUpdateAliveByNameSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateAliveByName(context.Background(), "bot1", true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAliveByNameError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.UpdateAliveByName(context.Background(), "bot1", false)
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// UpdateAgentSlackChannel
// --------------------------------------------------------------------------

func TestUpdateAgentSlackChannelSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateAgentSlackChannel(context.Background(), "bot1", "C12345"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAgentSlackChannelError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE openclaw_instances").WillReturnError(fmt.Errorf("db error"))
	err := doltDB.UpdateAgentSlackChannel(context.Background(), "bot1", "C12345")
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// GetAgentBySlackChannel
// --------------------------------------------------------------------------

func TestGetAgentBySlackChannelFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows([]string{"name"}).AddRow("bot1")
	mock.ExpectQuery("SELECT name FROM openclaw_instances").WillReturnRows(rows)
	name, err := doltDB.GetAgentBySlackChannel(context.Background(), "C12345")
	require.NoError(t, err)
	require.Equal(t, "bot1", name)
}

func TestGetAgentBySlackChannelNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows([]string{"name"})
	mock.ExpectQuery("SELECT name FROM openclaw_instances").WillReturnRows(rows)
	name, err := doltDB.GetAgentBySlackChannel(context.Background(), "CXXX")
	require.NoError(t, err)
	require.Equal(t, "", name)
}

func TestGetAgentBySlackChannelError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT name FROM openclaw_instances").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.GetAgentBySlackChannel(context.Background(), "C12345")
	require.Error(t, err)
	require.Contains(t, err.Error(), "looking up agent by slack channel")
}

// --------------------------------------------------------------------------
// Resource CRUD
// --------------------------------------------------------------------------

var resourceCols = []string{"id", "owner_id", "resource_type", "name", "resource_meta", "created_at", "updated_at"}

func TestCreateResourceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	r := Resource{
		ID: "r1", OwnerID: "u1", ResourceType: ResourceTypeGitHubRepo,
		Name: "myrepo", ResourceMeta: json.RawMessage(`{"url":"https://github.com/x/y"}`),
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, doltDB.CreateResource(context.Background(), r))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateResourceNilMeta(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO resources").WillReturnResult(sqlmock.NewResult(1, 1))
	r := Resource{ID: "r2", Name: "nometa", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, doltDB.CreateResource(context.Background(), r))
}

func TestCreateResourceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO resources").WillReturnError(fmt.Errorf("dup key"))
	r := Resource{ID: "r1", Name: "dup"}
	err := doltDB.CreateResource(context.Background(), r)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating resource")
}

func TestGetResourceFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(resourceCols).AddRow("r1", "u1", "github_repo", "myrepo", `{"url":"x"}`, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	r, err := doltDB.GetResource(context.Background(), "r1")
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "r1", r.ID)
	require.Equal(t, ResourceTypeGitHubRepo, r.ResourceType)
	require.Equal(t, json.RawMessage(`{"url":"x"}`), r.ResourceMeta)
}

func TestGetResourceNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(resourceCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	r, err := doltDB.GetResource(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Nil(t, r)
}

func TestGetResourceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))
	_, err := doltDB.GetResource(context.Background(), "r1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scanning resource")
}

func TestListResourcesByOwnerSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(resourceCols).
		AddRow("r1", "u1", "github_repo", "repo1", `{}`, now, now).
		AddRow("r2", "u1", "artifactory", "art1", `{}`, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	resources, err := doltDB.ListResourcesByOwner(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, resources, 2)
}

func TestListResourcesByOwnerEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(resourceCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	resources, err := doltDB.ListResourcesByOwner(context.Background(), "u1")
	require.NoError(t, err)
	require.Empty(t, resources)
}

func TestListResourcesByOwnerError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.ListResourcesByOwner(context.Background(), "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing resources")
}

func TestDeleteResourceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM resources").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.DeleteResource(context.Background(), "r1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteResourceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM resources").WillReturnError(fmt.Errorf("failed"))
	err := doltDB.DeleteResource(context.Background(), "r1")
	require.Error(t, err)
}

func TestUpdateResourceMetaSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE resources").WillReturnResult(sqlmock.NewResult(0, 1))
	err := doltDB.UpdateResourceMeta(context.Background(), "r1", json.RawMessage(`{"clone_url":"x"}`))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateResourceMetaError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE resources").WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.UpdateResourceMeta(context.Background(), "r1", json.RawMessage(`{}`))
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// Project CRUD
// --------------------------------------------------------------------------

var projectCols = []string{
	"id", "owner_id", "name", "description",
	"slack_channel_id", "slack_channel_name", "beads_prefix",
	"created_at", "updated_at",
}

func TestCreateProjectSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO projects").WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	p := Project{
		ID: "p1", OwnerID: "u1", Name: "TestProj", Description: "desc",
		BeadsPrefix: "TP", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, doltDB.CreateProject(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProjectError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO projects").WillReturnError(fmt.Errorf("dup"))
	p := Project{ID: "p1", Name: "dup"}
	err := doltDB.CreateProject(context.Background(), p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating project")
}

func TestGetProjectFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(projectCols).AddRow("p1", "u1", "TestProj", "desc", "C1", "test-proj", "TP", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetProject(context.Background(), "p1")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "TestProj", p.Name)
	require.Equal(t, "TP", p.BeadsPrefix)
}

func TestGetProjectNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(projectCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetProject(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Nil(t, p)
}

func TestGetProjectError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db down"))
	_, err := doltDB.GetProject(context.Background(), "p1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scanning project")
}

func TestGetProjectByBeadsPrefixFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(projectCols).AddRow("p1", "u1", "Proj", "", "", "", "AH", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetProjectByBeadsPrefix(context.Background(), "AH")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "AH", p.BeadsPrefix)
}

func TestGetProjectByBeadsPrefixNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(projectCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	p, err := doltDB.GetProjectByBeadsPrefix(context.Background(), "ZZ")
	require.NoError(t, err)
	require.Nil(t, p)
}

func TestListProjectsByOwnerSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(projectCols).
		AddRow("p1", "u1", "Proj1", "", "", "", "AH", now, now).
		AddRow("p2", "u1", "Proj2", "", "", "", "BH", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	projects, err := doltDB.ListProjectsByOwner(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, projects, 2)
}

func TestListProjectsByOwnerError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("db error"))
	_, err := doltDB.ListProjectsByOwner(context.Background(), "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing projects")
}

func TestListAllProjectsSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(projectCols).AddRow("p1", "u1", "Proj1", "", "", "", "AH", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	projects, err := doltDB.ListAllProjects(context.Background())
	require.NoError(t, err)
	require.Len(t, projects, 1)
}

func TestListAllProjectsError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("conn lost"))
	_, err := doltDB.ListAllProjects(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing all projects")
}

func TestUpdateProjectSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE projects").WillReturnResult(sqlmock.NewResult(0, 1))
	p := Project{ID: "p1", Name: "Updated"}
	require.NoError(t, doltDB.UpdateProject(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProjectError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE projects").WillReturnError(fmt.Errorf("update failed"))
	err := doltDB.UpdateProject(context.Background(), Project{ID: "p1"})
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// Project Resources
// --------------------------------------------------------------------------

func TestAddProjectResourceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT IGNORE INTO project_resources").WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, doltDB.AddProjectResource(context.Background(), "p1", "r1", true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddProjectResourceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT IGNORE INTO project_resources").WillReturnError(fmt.Errorf("err"))
	err := doltDB.AddProjectResource(context.Background(), "p1", "r1", false)
	require.Error(t, err)
}

func TestRemoveProjectResourceSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM project_resources").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.RemoveProjectResource(context.Background(), "p1", "r1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoveProjectResourceError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM project_resources").WillReturnError(fmt.Errorf("err"))
	err := doltDB.RemoveProjectResource(context.Background(), "p1", "r1")
	require.Error(t, err)
}

func TestListProjectResourcesSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"project_id", "resource_id", "is_primary", "added_at"}).
		AddRow("p1", "r1", 1, now).
		AddRow("p1", "r2", 0, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	prs, err := doltDB.ListProjectResources(context.Background(), "p1")
	require.NoError(t, err)
	require.Len(t, prs, 2)
	require.True(t, prs[0].IsPrimary)
	require.False(t, prs[1].IsPrimary)
}

func TestListProjectResourcesEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows([]string{"project_id", "resource_id", "is_primary", "added_at"})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	prs, err := doltDB.ListProjectResources(context.Background(), "p1")
	require.NoError(t, err)
	require.Empty(t, prs)
}

func TestListProjectResourcesError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("err"))
	_, err := doltDB.ListProjectResources(context.Background(), "p1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing project resources")
}

// --------------------------------------------------------------------------
// Project Agents
// --------------------------------------------------------------------------

func TestAddProjectAgentSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT IGNORE INTO project_agents").WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, doltDB.AddProjectAgent(context.Background(), "p1", "a1", "admin"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddProjectAgentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT IGNORE INTO project_agents").WillReturnError(fmt.Errorf("err"))
	err := doltDB.AddProjectAgent(context.Background(), "p1", "a1", "admin")
	require.Error(t, err)
}

func TestRemoveProjectAgentSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM project_agents").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.RemoveProjectAgent(context.Background(), "p1", "a1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoveProjectAgentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("DELETE FROM project_agents").WillReturnError(fmt.Errorf("err"))
	err := doltDB.RemoveProjectAgent(context.Background(), "p1", "a1")
	require.Error(t, err)
}

func TestListProjectAgentsSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"project_id", "agent_id", "granted_by", "granted_at"}).
		AddRow("p1", "a1", "admin", now).
		AddRow("p1", "a2", "system", now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	agents, err := doltDB.ListProjectAgents(context.Background(), "p1")
	require.NoError(t, err)
	require.Len(t, agents, 2)
	require.Equal(t, "a1", agents[0].AgentID)
	require.Equal(t, "admin", agents[0].GrantedBy)
}

func TestListProjectAgentsError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("err"))
	_, err := doltDB.ListProjectAgents(context.Background(), "p1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing project agents")
}

// --------------------------------------------------------------------------
// Task Assignments
// --------------------------------------------------------------------------

var assignmentCols = []string{
	"id", "task_id", "project_id", "agent_id", "assigned_by", "assigned_at", "revoked_at",
}

func TestCreateTaskAssignmentSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO task_assignments").WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	ta := TaskAssignment{
		ID: "ta1", TaskID: "AH-1", ProjectID: "p1",
		AgentID: "a1", AssignedBy: "admin", AssignedAt: now,
	}
	require.NoError(t, doltDB.CreateTaskAssignment(context.Background(), ta))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTaskAssignmentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO task_assignments").WillReturnError(fmt.Errorf("dup"))
	ta := TaskAssignment{ID: "ta1"}
	err := doltDB.CreateTaskAssignment(context.Background(), ta)
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating task assignment")
}

func TestGetTaskAssignmentFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(assignmentCols).AddRow("ta1", "AH-1", "p1", "a1", "admin", now, nil)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetTaskAssignment(context.Background(), "ta1")
	require.NoError(t, err)
	require.NotNil(t, ta)
	require.Equal(t, "ta1", ta.ID)
	require.Equal(t, "AH-1", ta.TaskID)
	require.Nil(t, ta.RevokedAt)
}

func TestGetTaskAssignmentNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(assignmentCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetTaskAssignment(context.Background(), "none")
	require.NoError(t, err)
	require.Nil(t, ta)
}

func TestGetTaskAssignmentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("err"))
	_, err := doltDB.GetTaskAssignment(context.Background(), "ta1")
	require.Error(t, err)
}

func TestGetActiveAssignmentByTaskAndAgentFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(assignmentCols).AddRow("ta1", "AH-1", "p1", "a1", "admin", now, nil)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetActiveAssignmentByTaskAndAgent(context.Background(), "AH-1", "a1")
	require.NoError(t, err)
	require.NotNil(t, ta)
}

func TestGetActiveAssignmentByTaskAndAgentNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(assignmentCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetActiveAssignmentByTaskAndAgent(context.Background(), "AH-1", "a1")
	require.NoError(t, err)
	require.Nil(t, ta)
}

func TestGetActiveAssignmentByTaskFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(assignmentCols).AddRow("ta1", "AH-1", "p1", "a1", "admin", now, nil)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetActiveAssignmentByTask(context.Background(), "AH-1")
	require.NoError(t, err)
	require.NotNil(t, ta)
}

func TestGetActiveAssignmentByTaskNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(assignmentCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	ta, err := doltDB.GetActiveAssignmentByTask(context.Background(), "AH-999")
	require.NoError(t, err)
	require.Nil(t, ta)
}

func TestRevokeTaskAssignmentSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE task_assignments").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.RevokeTaskAssignment(context.Background(), "ta1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeTaskAssignmentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE task_assignments").WillReturnError(fmt.Errorf("err"))
	err := doltDB.RevokeTaskAssignment(context.Background(), "ta1")
	require.Error(t, err)
}

func TestListActiveAssignmentsByAgentSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(assignmentCols).
		AddRow("ta1", "AH-1", "p1", "a1", "admin", now, nil).
		AddRow("ta2", "AH-2", "p1", "a1", "system", now, nil)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	assignments, err := doltDB.ListActiveAssignmentsByAgent(context.Background(), "a1")
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestListActiveAssignmentsByAgentEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(assignmentCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	assignments, err := doltDB.ListActiveAssignmentsByAgent(context.Background(), "a1")
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestListActiveAssignmentsByAgentError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("err"))
	_, err := doltDB.ListActiveAssignmentsByAgent(context.Background(), "a1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing assignments")
}

// --------------------------------------------------------------------------
// User CRUD
// --------------------------------------------------------------------------

var userCols = []string{"id", "username", "email", "role", "api_token", "created_at", "updated_at"}

func TestCreateUserSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	now := time.Now().UTC()
	u := User{ID: "u1", Username: "alice", Email: "a@x.com", Role: "admin", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, doltDB.CreateUser(context.Background(), u))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateUserError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO users").WillReturnError(fmt.Errorf("dup"))
	err := doltDB.CreateUser(context.Background(), User{ID: "u1", Username: "dup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating user")
}

func TestGetUserByUsernameFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(userCols).AddRow("u1", "alice", "a@x.com", "admin", "", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	u, err := doltDB.GetUserByUsername(context.Background(), "alice")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.Equal(t, "alice", u.Username)
}

func TestGetUserByUsernameNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(userCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	u, err := doltDB.GetUserByUsername(context.Background(), "ghost")
	require.NoError(t, err)
	require.Nil(t, u)
}

func TestGetUserByIDFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows(userCols).AddRow("u1", "alice", "a@x.com", "admin", "", now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	u, err := doltDB.GetUserByID(context.Background(), "u1")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.Equal(t, "u1", u.ID)
}

func TestGetUserByIDNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	rows := sqlmock.NewRows(userCols)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	u, err := doltDB.GetUserByID(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Nil(t, u)
}

func TestUpdateUserAPITokenSuccess(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE users").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, doltDB.UpdateUserAPIToken(context.Background(), "u1", "hashed-token"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateUserAPITokenError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("UPDATE users").WillReturnError(fmt.Errorf("err"))
	err := doltDB.UpdateUserAPIToken(context.Background(), "u1", "tok")
	require.Error(t, err)
}

func TestEnsureAdminUserCreatesWhenEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT IGNORE INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, doltDB.EnsureAdminUser(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdminUserSkipsWhenExists(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	require.NoError(t, doltDB.EnsureAdminUser(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureAdminUserCountError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("db error"))
	err := doltDB.EnsureAdminUser(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "counting users")
}

// --------------------------------------------------------------------------
// IsInitialised
// --------------------------------------------------------------------------

func TestIsInitialisedTrue(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	init, err := IsInitialised(doltDB)
	require.NoError(t, err)
	require.True(t, init)
}

func TestIsInitialisedFalse(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	init, err := IsInitialised(doltDB)
	require.NoError(t, err)
	require.False(t, init)
}

func TestIsInitialisedError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("table not found"))
	_, err := IsInitialised(doltDB)
	require.Error(t, err)
}

// --------------------------------------------------------------------------
// DoltPersister
// --------------------------------------------------------------------------

func TestNewDoltPersisterBadKeyLen(t *testing.T) {
	doltDB, _ := newMockDB(t)
	_, err := NewDoltPersister(doltDB, []byte("short"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "32 bytes")
}

func TestDoltPersisterSetAndGet(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, p.Set("test_key", "test_value"))

	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	v, err := p.Get("test_key")
	require.NoError(t, err)
	require.Equal(t, "", v)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltPersisterSetSaltKeyUnencrypted(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, p.Set("_salt", "abc123"))

	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("abc123"))
	v, err := p.Get("_salt")
	require.NoError(t, err)
	require.Equal(t, "abc123", v)
}

func TestDoltPersisterDelete(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectExec("DELETE FROM settings").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, p.Delete("some_key"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDoltPersisterDeleteError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectExec("DELETE FROM settings").WillReturnError(fmt.Errorf("err"))
	err = p.Delete("key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "deleting setting")
}

func TestDoltPersisterKeys(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	rows := sqlmock.NewRows([]string{"key_name"}).AddRow("foo").AddRow("bar")
	mock.ExpectQuery("SELECT key_name FROM settings").WillReturnRows(rows)
	keys := p.Keys()
	require.ElementsMatch(t, []string{"foo", "bar"}, keys)
}

func TestDoltPersisterKeysEmpty(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	rows := sqlmock.NewRows([]string{"key_name"})
	mock.ExpectQuery("SELECT key_name FROM settings").WillReturnRows(rows)
	keys := p.Keys()
	require.Empty(t, keys)
}

func TestDoltPersisterKeysQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT key_name FROM settings").WillReturnError(fmt.Errorf("err"))
	keys := p.Keys()
	require.Nil(t, keys)
}

func TestDoltPersisterSetError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectExec("INSERT INTO settings").WillReturnError(fmt.Errorf("db error"))
	err = p.Set("foo", "bar")
	require.Error(t, err)
	require.Contains(t, err.Error(), "storing setting")
}

func TestDoltPersisterGetQueryError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	key := make([]byte, 32)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT value FROM settings").WillReturnError(fmt.Errorf("conn lost"))
	_, err = p.Get("key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "querying setting")
}

// --------------------------------------------------------------------------
// Encrypt/Decrypt round-trip
// --------------------------------------------------------------------------

func TestDoltPersisterEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	doltDB, mock := newMockDB(t)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	var storedValue string
	mock.ExpectExec("INSERT INTO settings").
		WillReturnResult(sqlmock.NewResult(1, 1)).
		WillDelayFor(0)
	require.NoError(t, p.Set("secret", "my-secret-value"))

	enc, err := p.encrypt("my-secret-value")
	require.NoError(t, err)
	require.NotEqual(t, "my-secret-value", enc)
	storedValue = enc

	dec, err := p.decrypt(storedValue)
	require.NoError(t, err)
	require.Equal(t, "my-secret-value", dec)
}

// --------------------------------------------------------------------------
// OpenDoltPersister — reads existing salt
// --------------------------------------------------------------------------

func TestOpenDoltPersisterExistingSalt(t *testing.T) {
	doltDB, mock := newMockDB(t)
	saltHex := "0102030405060708090a0b0c0d0e0f10"
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(saltHex))
	p, err := OpenDoltPersister(doltDB, "testpassword")
	require.NoError(t, err)
	require.NotNil(t, p)
}

func TestOpenDoltPersisterNewSalt(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO settings").
		WillReturnResult(sqlmock.NewResult(1, 1))
	p, err := OpenDoltPersister(doltDB, "testpassword")
	require.NoError(t, err)
	require.NotNil(t, p)
}

func TestOpenDoltPersisterSaltReadError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnError(fmt.Errorf("db connection lost"))
	_, err := OpenDoltPersister(doltDB, "testpassword")
	require.Error(t, err)
	require.Contains(t, err.Error(), "salt")
}

// --------------------------------------------------------------------------
// MigrateFrom
// --------------------------------------------------------------------------

type mockMigrationSource struct {
	data map[string]string
}

func (m *mockMigrationSource) Keys() []string {
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys
}

func (m *mockMigrationSource) Get(key string) (string, error) {
	return m.data[key], nil
}

func TestMigrateFromSuccess(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	doltDB, mock := newMockDB(t)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	src := &mockMigrationSource{data: map[string]string{
		"openai_api_key": "sk-test",
	}}

	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("openai_api_key").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO settings").
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, p.MigrateFrom(src))
}

func TestMigrateFromSkipsSalt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	doltDB, _ := newMockDB(t)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	src := &mockMigrationSource{data: map[string]string{
		"_salt": "should-be-skipped",
	}}

	require.NoError(t, p.MigrateFrom(src))
}

func TestMigrateFromSkipsExisting(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	doltDB, mock := newMockDB(t)
	p, err := NewDoltPersister(doltDB, key)
	require.NoError(t, err)

	src := &mockMigrationSource{data: map[string]string{
		"existing_key": "val",
	}}

	enc, _ := p.encrypt("already-here")
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("existing_key").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(enc))

	require.NoError(t, p.MigrateFrom(src))
}

// --------------------------------------------------------------------------
// ensureSalt / readSaltRaw / storeSaltRaw
// --------------------------------------------------------------------------

func TestReadSaltRawFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("abcdef0123456789"))

	raw, err := readSaltRaw(doltDB)
	require.NoError(t, err)
	require.Equal(t, "abcdef0123456789", raw)
}

func TestReadSaltRawNotFound(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnError(sql.ErrNoRows)

	raw, err := readSaltRaw(doltDB)
	require.NoError(t, err)
	require.Equal(t, "", raw)
}

func TestReadSaltRawError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnError(fmt.Errorf("disk error"))

	_, err := readSaltRaw(doltDB)
	require.Error(t, err)
}

func TestStoreSaltRaw(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO settings").
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, storeSaltRaw(doltDB, "aabbccdd"))
}

func TestStoreSaltRawError(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectExec("INSERT INTO settings").
		WillReturnError(fmt.Errorf("table locked"))

	require.Error(t, storeSaltRaw(doltDB, "aabbccdd"))
}

func TestEnsureSaltExisting(t *testing.T) {
	doltDB, mock := newMockDB(t)
	saltHex := "0102030405060708090a0b0c0d0e0f10"
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(saltHex))

	salt, err := ensureSalt(doltDB)
	require.NoError(t, err)
	require.Len(t, salt, 16)
}

func TestEnsureSaltGenerate(t *testing.T) {
	doltDB, mock := newMockDB(t)
	mock.ExpectQuery("SELECT value FROM settings WHERE key_name").
		WithArgs("_salt").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO settings").
		WillReturnResult(sqlmock.NewResult(1, 1))

	salt, err := ensureSalt(doltDB)
	require.NoError(t, err)
	require.Len(t, salt, 16)
}
