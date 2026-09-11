package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
)

func TestBusinessWorkspaceCoreCRUDAndReferences(t *testing.T) {
	p, admin, a, owner := workspaceFixture(t)
	b, err := p.CreateWorkspace(admin, "B", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	u := workspaceUser(t, p, "business-member")
	actorB, _, err := p.ResolveWorkspaceAccess(admin, b.ID, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	contexts := []context.Context{workspaceMember(t, p, admin, a.ID, u, "member"), workspaceMember(t, p, service.WithAccessPrincipal(admin, actorB), b.ID, u, "member")}
	type records struct {
		agent    *service.Agent
		org      *service.Organization
		goal     *service.Goal
		project  *service.Project
		task     *service.Task
		label    *service.Label
		comment  *service.IssueComment
		approval *service.Approval
		join     *service.OrganizationAgent
	}
	var rows []records
	for _, ctx := range contexts {
		var v records
		v.agent, err = p.CreateAgent(ctx, service.Agent{Name: "same"})
		if err != nil {
			t.Fatal(err)
		}
		v.org, err = p.CreateOrganization(ctx, service.Organization{Name: "same", HeadAgentID: v.agent.ID})
		if err != nil {
			t.Fatal(err)
		}
		v.goal, err = p.CreateGoal(ctx, service.Goal{Name: "same", Status: "active", OrganizationID: v.org.ID})
		if err != nil {
			t.Fatal(err)
		}
		v.project, err = p.CreateProject(ctx, service.Project{Name: "same", Status: "active", OrganizationID: v.org.ID, GoalID: v.goal.ID, LeadAgentID: v.agent.ID})
		if err != nil {
			t.Fatal(err)
		}
		v.task, err = p.CreateTask(ctx, service.Task{Title: "same", Status: "open", OrganizationID: v.org.ID, ProjectID: v.project.ID, GoalID: v.goal.ID, AssignedAgentID: v.agent.ID})
		if err != nil {
			t.Fatal(err)
		}
		v.label, err = p.CreateLabel(ctx, service.Label{Name: "same", OrganizationID: v.org.ID})
		if err != nil {
			t.Fatal(err)
		}
		v.comment, err = p.CreateComment(ctx, service.IssueComment{TaskID: v.task.ID, Body: "private to workspace"})
		if err != nil {
			t.Fatal(err)
		}
		v.approval, err = p.CreateApproval(ctx, service.Approval{OrganizationID: v.org.ID, Type: "hire_agent", Status: "pending", RequestDetails: map[string]any{"agent_id": v.agent.ID}})
		if err != nil {
			t.Fatal(err)
		}
		v.join, err = p.CreateOrganizationAgent(ctx, service.OrganizationAgent{OrganizationID: v.org.ID, AgentID: v.agent.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err = p.AddLabelToTask(ctx, v.task.ID, v.label.ID); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, v)
	}
	ctx := contexts[0]
	own, foreign := rows[0], rows[1]
	q := query.New().SetLimit(1)
	agents, err := p.ListAgents(ctx, q)
	if err != nil || agents.Meta.Total != 1 || len(agents.Data) != 1 || agents.Data[0].ID != own.agent.ID {
		t.Fatalf("agent list/count: %+v %v", agents, err)
	}
	orgs, err := p.ListOrganizations(ctx, q)
	if err != nil || orgs.Meta.Total != 1 || orgs.Data[0].ID != own.org.ID {
		t.Fatalf("org list/count: %+v %v", orgs, err)
	}
	goals, err := p.ListGoals(ctx, q)
	if err != nil || goals.Meta.Total != 1 || goals.Data[0].ID != own.goal.ID {
		t.Fatalf("goal list/count: %+v %v", goals, err)
	}
	projects, err := p.ListProjects(ctx, q)
	if err != nil || projects.Meta.Total != 1 || projects.Data[0].ID != own.project.ID {
		t.Fatalf("project list/count: %+v %v", projects, err)
	}
	tasks, err := p.ListTasks(ctx, q)
	if err != nil || tasks.Meta.Total != 1 || tasks.Data[0].ID != own.task.ID {
		t.Fatalf("task list/count: %+v %v", tasks, err)
	}
	approvals, err := p.ListApprovals(ctx, q)
	if err != nil || approvals.Meta.Total != 1 || approvals.Data[0].ID != own.approval.ID {
		t.Fatalf("approval list/count: %+v %v", approvals, err)
	}
	if v, e := p.GetAgent(ctx, foreign.agent.ID); e != nil || v != nil {
		t.Fatalf("foreign agent: %+v %v", v, e)
	}
	if v, e := p.GetOrganization(ctx, foreign.org.ID); e != nil || v != nil {
		t.Fatalf("foreign org: %+v %v", v, e)
	}
	if v, e := p.GetGoal(ctx, foreign.goal.ID); e != nil || v != nil {
		t.Fatalf("foreign goal: %+v %v", v, e)
	}
	if v, e := p.GetProject(ctx, foreign.project.ID); e != nil || v != nil {
		t.Fatalf("foreign project: %+v %v", v, e)
	}
	if v, e := p.GetTask(ctx, foreign.task.ID); e != nil || v != nil {
		t.Fatalf("foreign task: %+v %v", v, e)
	}
	if v, e := p.GetComment(ctx, foreign.comment.ID); e != nil || v != nil {
		t.Fatalf("foreign comment: %+v %v", v, e)
	}
	if v, e := p.GetLabel(ctx, foreign.label.ID); e != nil || v != nil {
		t.Fatalf("foreign label: %+v %v", v, e)
	}
	if v, e := p.GetApproval(ctx, foreign.approval.ID); e != nil || v != nil {
		t.Fatalf("foreign approval: %+v %v", v, e)
	}
	if v, e := p.GetOrganizationAgent(ctx, foreign.join.ID); e != nil || v != nil {
		t.Fatalf("foreign org agent: %+v %v", v, e)
	}
	if _, err = p.CreateTask(ctx, service.Task{Title: "bad", Status: "open", ProjectID: foreign.project.ID}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign task reference: %v", err)
	}
	if _, err = p.CreateProject(ctx, service.Project{Name: "bad", GoalID: foreign.goal.ID}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign project reference: %v", err)
	}
	if _, err = p.CreateOrganizationAgent(ctx, service.OrganizationAgent{OrganizationID: own.org.ID, AgentID: foreign.agent.ID}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign agent association: %v", err)
	}
	if err = p.AddLabelToTask(ctx, own.task.ID, foreign.label.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign label association: %v", err)
	}
	if _, err = p.CreateComment(ctx, service.IssueComment{TaskID: own.task.ID, ParentID: foreign.comment.ID}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign comment parent: %v", err)
	}
	if _, err = p.CreateApproval(ctx, service.Approval{RequestDetails: map[string]any{"agent_id": foreign.agent.ID}}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign approval reference: %v", err)
	}
	labels, err := p.ListLabelsForTask(ctx, own.task.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != own.label.ID {
		t.Fatalf("scoped label join: %+v %v", labels, err)
	}
	ids, err := p.ListTasksForLabel(ctx, own.label.ID)
	if err != nil || len(ids) != 1 || ids[0] != own.task.ID {
		t.Fatalf("scoped task join: %+v %v", ids, err)
	}
	if _, err = p.ListLabelsForTask(ctx, foreign.task.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign task join: %v", err)
	}
	if err = p.DeleteAgent(ctx, foreign.agent.ID); err != nil {
		t.Fatal(err)
	}
	if v, e := p.GetAgent(contexts[1], foreign.agent.ID); e != nil || v == nil {
		t.Fatalf("cross delete removed foreign agent: %v", e)
	}
	if err = p.CheckoutTask(ctx, own.task.ID, foreign.agent.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign checkout: %v", err)
	}
	if err = p.CheckoutTask(ctx, own.task.ID, own.agent.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.ReleaseTask(ctx, own.task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ListTasks(t.Context(), nil); !errors.Is(err, service.ErrWorkspaceRequired) {
		t.Fatalf("absent scope list: %v", err)
	}
	if _, err = p.CreateAgent(t.Context(), service.Agent{Name: "unscoped"}); !errors.Is(err, service.ErrWorkspaceRequired) {
		t.Fatalf("absent scope write: %v", err)
	}
}
