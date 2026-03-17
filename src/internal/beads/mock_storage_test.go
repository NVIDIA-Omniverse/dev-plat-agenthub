package beads

import (
	"context"
	"fmt"

	beadslib "github.com/steveyegge/beads"
)

// mockStorage implements the narrow Storage interface for unit tests.
type mockStorage struct {
	issues          map[string]*beadslib.Issue
	configs         map[string]string
	comments        map[string][]*beadslib.Comment
	lastUpdates     map[string]interface{}
	createErr       error
	updateErr       error
	closeErr        error
	getErr          error
	searchErr       error
	readyWorkErr    error
	commentErr      error
	setConfigErr    error
	getConfigErr    error
	setConfigCalls  int
	setConfigFailAt int // fail on the Nth call (1-based), 0 = use setConfigErr always
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		issues:   make(map[string]*beadslib.Issue),
		configs:  make(map[string]string),
		comments: make(map[string][]*beadslib.Comment),
	}
}

var _ Storage = (*mockStorage)(nil) // compile-time interface check

func (m *mockStorage) CreateIssue(_ context.Context, issue *beadslib.Issue, _ string) error {
	if m.createErr != nil {
		return m.createErr
	}
	if issue.ID == "" {
		issue.ID = "mock-" + fmt.Sprintf("%d", len(m.issues))
	}
	m.issues[issue.ID] = issue
	return nil
}

func (m *mockStorage) GetIssue(_ context.Context, id string) (*beadslib.Issue, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if issue, ok := m.issues[id]; ok {
		return issue, nil
	}
	return nil, fmt.Errorf("not found: %s", id)
}

func (m *mockStorage) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	m.lastUpdates = updates
	if m.updateErr != nil {
		return m.updateErr
	}
	issue, ok := m.issues[id]
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}
	if assignee, ok := updates["assignee"].(string); ok {
		issue.Assignee = assignee
	}
	if status, ok := updates["status"].(beadslib.Status); ok {
		issue.Status = status
	}
	return nil
}

func (m *mockStorage) CloseIssue(_ context.Context, id, reason, _, _ string) error {
	if m.closeErr != nil {
		return m.closeErr
	}
	issue, ok := m.issues[id]
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}
	issue.Status = beadslib.StatusClosed
	issue.CloseReason = reason
	return nil
}

func (m *mockStorage) SearchIssues(_ context.Context, _ string, _ beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	issues := make([]*beadslib.Issue, 0, len(m.issues))
	for _, v := range m.issues {
		issues = append(issues, v)
	}
	return issues, nil
}

func (m *mockStorage) GetReadyWork(_ context.Context, _ beadslib.WorkFilter) ([]*beadslib.Issue, error) {
	if m.readyWorkErr != nil {
		return nil, m.readyWorkErr
	}
	var ready []*beadslib.Issue
	for _, v := range m.issues {
		if v.Status == beadslib.StatusOpen && v.Assignee == "" {
			ready = append(ready, v)
		}
	}
	return ready, nil
}

func (m *mockStorage) AddIssueComment(_ context.Context, issueID, author, text string) (*beadslib.Comment, error) {
	if m.commentErr != nil {
		return nil, m.commentErr
	}
	c := &beadslib.Comment{Author: author, Text: text}
	m.comments[issueID] = append(m.comments[issueID], c)
	return c, nil
}

func (m *mockStorage) SetConfig(_ context.Context, key, value string) error {
	m.setConfigCalls++
	if m.setConfigFailAt > 0 && m.setConfigCalls >= m.setConfigFailAt {
		return m.setConfigErr
	}
	if m.setConfigErr != nil && m.setConfigFailAt == 0 {
		return m.setConfigErr
	}
	m.configs[key] = value
	return nil
}

func (m *mockStorage) GetConfig(_ context.Context, key string) (string, error) {
	if m.getConfigErr != nil {
		return "", m.getConfigErr
	}
	return m.configs[key], nil
}
