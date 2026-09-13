package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

func normalizeScopedTrigger(t service.Trigger) (service.Trigger, error) {
	if t.TargetType == "" {
		t.TargetType = service.TriggerTargetWorkflow
	}
	if t.TargetType != service.TriggerTargetWorkflow {
		return t, service.ErrAccessDenied
	}
	if t.TargetID == "" {
		t.TargetID = t.WorkflowID
	}
	if t.TargetID == "" || t.WorkflowID != "" && t.WorkflowID != t.TargetID {
		return t, service.ErrAccessResourceNotFound
	}
	t.WorkflowID = t.TargetID
	return t, nil
}
func (p *Postgres) triggerReferences(ctx context.Context, w *businessWrite, t service.Trigger) error {
	if t.WorkspaceID != "" && t.WorkspaceID != w.actor.WorkspaceID {
		return service.ErrAccessDenied
	}
	if !w.actor.Allows("workflows.write", service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: t.WorkflowID}) {
		return service.ErrAccessDenied
	}
	if err := p.businessReference(ctx, w, p.tableWorkflows, "id", t.WorkflowID); err != nil {
		return err
	}
	if t.EntryNodeID != "" {
		var raw string
		_, err := w.tx.From(p.tableWorkflows).Select("graph").Where(w.predicate, goqu.C("id").Eq(t.WorkflowID)).ScanValContext(ctx, &raw)
		if err != nil {
			return fmt.Errorf("read trigger entry graph: %w", err)
		}
		var graph service.WorkflowGraph
		if json.Unmarshal([]byte(raw), &graph) != nil {
			return service.ErrWorkspaceConflict
		}
		found := false
		for _, node := range graph.Nodes {
			if node.ID == t.EntryNodeID && node.Type == "input" {
				found = true
			}
		}
		if !found {
			return service.ErrAccessResourceNotFound
		}
	}
	return nil
}
func (p *Postgres) beginTriggerWrite(ctx context.Context, id string) (*businessWrite, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableTriggers, "workspace.read", "")
	if err != nil {
		return nil, err
	}
	var target string
	found, err := w.tx.From(p.tableTriggers).Select("target_id").Where(w.predicate, goqu.C("id").Eq(id)).ForUpdate(goqu.Wait).ScanValContext(ctx, &target)
	if err != nil {
		w.tx.Rollback()
		return nil, fmt.Errorf("lock trigger: %w", err)
	}
	if !found {
		w.tx.Rollback()
		return nil, service.ErrAccessResourceNotFound
	}
	if !w.actor.Allows("workflows.write", service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: target}) {
		w.tx.Rollback()
		return nil, service.ErrAccessDenied
	}
	return w, nil
}

func (p *Postgres) orgAgentReadScope(ctx context.Context) (exp.Expression, error) {
	a, err := p.businessReadScope(ctx, p.tableOrganizationAgents)
	if err != nil {
		return nil, err
	}
	o, err := p.businessReadScope(ctx, p.tableOrganizations)
	if err != nil {
		return nil, err
	}
	return goqu.And(a, goqu.C("organization_id").In(p.goqu.From(p.tableOrganizations).Select("id").Where(o))), nil
}
func (p *Postgres) beginOrgAgentWrite(ctx context.Context, id string) (*businessWrite, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableOrganizationAgents, "workspace.read", "")
	if err != nil {
		return nil, err
	}
	var row struct {
		OrganizationID string `db:"organization_id"`
	}
	found, err := w.tx.From(p.tableOrganizationAgents).Where(w.predicate, goqu.C("id").Eq(id)).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		w.tx.Rollback()
		return nil, fmt.Errorf("lock organization agent: %w", err)
	}
	if !found {
		w.tx.Rollback()
		return nil, service.ErrAccessResourceNotFound
	}
	if !w.actor.Allows("organizations.write", service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: row.OrganizationID}) {
		w.tx.Rollback()
		return nil, service.ErrAccessDenied
	}
	return w, nil
}
func (p *Postgres) orgAgentReferences(ctx context.Context, w *businessWrite, v service.OrganizationAgent) error {
	if v.OrganizationID == "" || v.AgentID == "" {
		return service.ErrAccessResourceNotFound
	}
	if err := p.businessReference(ctx, w, p.tableOrganizations, "id", v.OrganizationID); err != nil {
		return err
	}
	if err := p.businessReference(ctx, w, p.tableAgents, "id", v.AgentID); err != nil {
		return err
	}
	return p.businessReference(ctx, w, p.tableAgents, "id", v.ParentAgentID)
}

func (p *Postgres) businessNamedReference(ctx context.Context, w *businessWrite, table interface{}, column, value string) error {
	if value == "" {
		return nil
	}
	var ids []string
	err := w.tx.From(table).Select("id").Where(goqu.C("workspace_id").Eq(w.actor.WorkspaceID), goqu.Or(goqu.C("id").Eq(value), goqu.C(column).Eq(value))).ForKeyShare(goqu.Wait).Limit(2).ScanValsContext(ctx, &ids)
	if err != nil {
		return fmt.Errorf("validate named workspace reference: %w", err)
	}
	if len(ids) != 1 {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) agentReferences(ctx context.Context, w *businessWrite, c service.AgentConfig) error {
	if len(c.MCPs) > 0 && !w.actor.PlatformAdmin {
		return service.ErrAccessDenied
	}
	if err := p.businessProviderReference(ctx, w, c.Provider); err != nil {
		return err
	}
	for _, skill := range c.Skills {
		if err := p.businessNamedReference(ctx, w, p.tableSkills, "name", skill.ID); err != nil {
			return err
		}
		for _, id := range skill.Connections {
			if err := p.businessReference(ctx, w, p.tableConnections, "id", id); err != nil {
				return err
			}
		}
	}
	for _, name := range c.MCPSets {
		if err := p.businessNamedReference(ctx, w, p.tableMCPSets, "name", name); err != nil {
			return err
		}
	}
	for _, name := range c.Workflows {
		if err := p.businessNamedReference(ctx, w, p.tableWorkflows, "name", name); err != nil {
			return err
		}
	}
	for _, id := range c.Connections {
		if err := p.businessReference(ctx, w, p.tableConnections, "id", id); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) businessProviderReference(ctx context.Context, w *businessWrite, key string) error {
	if key == "" {
		return nil
	}
	var id string
	found, err := w.tx.From(p.tableProviders).Select("id").Where(goqu.C("key").Eq(key), goqu.Or(goqu.C("workspace_id").Eq(w.actor.WorkspaceID), goqu.And(goqu.C("workspace_id").Eq("legacy-default"), goqu.Or(goqu.L("config->>'shared_with_all_workspaces' = 'true'"), goqu.C("id").In(w.tx.From(p.workspaceTable("workspace_provider_grants")).Select("provider_id").Where(goqu.Ex{"workspace_id": w.actor.WorkspaceID})))))).Limit(1).ForKeyShare(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("validate provider binding: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) workflowReferences(ctx context.Context, w *businessWrite, g service.WorkflowGraph) error {
	nodes := map[string]bool{}
	for _, node := range g.Nodes {
		if node.ID == "" || nodes[node.ID] {
			return service.ErrWorkspaceConflict
		}
		nodes[node.ID] = true
	}
	for _, edge := range g.Edges {
		if !nodes[edge.Source] || !nodes[edge.Target] {
			return service.ErrWorkspaceConflict
		}
	}
	for _, node := range g.Nodes {
		if node.ParentID != "" && !nodes[node.ParentID] {
			return service.ErrWorkspaceConflict
		}
		for _, ref := range []struct {
			key   string
			table interface{}
		}{{"agent_id", p.tableAgents}, {"workflow_id", p.tableWorkflows}, {"config_id", p.tableNodeConfigs}, {"connection_id", p.tableConnections}} {
			if raw, ok := node.Data[ref.key]; ok {
				id, ok := raw.(string)
				if !ok {
					return service.ErrAccessDenied
				}
				if err := p.businessReference(ctx, w, ref.table, "id", id); err != nil {
					return err
				}
			}
		}
		if raw, ok := node.Data["provider"]; ok {
			key, ok := raw.(string)
			if !ok {
				return service.ErrAccessDenied
			}
			if err := p.businessProviderReference(ctx, w, key); err != nil {
				return err
			}
		}
		for _, ref := range []struct {
			key   string
			table interface{}
		}{{"skills", p.tableSkills}, {"mcp_sets", p.tableMCPSets}, {"workflows", p.tableWorkflows}} {
			if raw, ok := node.Data[ref.key]; ok {
				values, err := businessStringList(raw)
				if err != nil {
					return err
				}
				for _, v := range values {
					if err = p.businessNamedReference(ctx, w, ref.table, "name", v); err != nil {
						return err
					}
				}
			}
		}
		if raw, ok := node.Data["mcp_urls"]; ok {
			values, err := businessStringList(raw)
			if err != nil {
				return err
			}
			if len(values) > 0 && !w.actor.PlatformAdmin {
				return service.ErrAccessDenied
			}
		}
	}
	return nil
}
func businessStringList(value any) ([]string, error) {
	switch values := value.(type) {
	case []string:
		return values, nil
	case []any:
		out := make([]string, 0, len(values))
		for _, v := range values {
			str, ok := v.(string)
			if !ok {
				return nil, service.ErrAccessDenied
			}
			out = append(out, str)
		}
		return out, nil
	case nil:
		return nil, nil
	default:
		return nil, service.ErrAccessDenied
	}
}

func (p *Postgres) goalReferences(ctx context.Context, w *businessWrite, g service.Goal) error {
	if err := p.businessReference(ctx, w, p.tableOrganizations, "id", g.OrganizationID); err != nil {
		return err
	}
	return p.businessReference(ctx, w, p.tableGoals, "id", g.ParentGoalID)
}

func (p *Postgres) projectReferences(ctx context.Context, w *businessWrite, v service.Project) error {
	if err := p.businessReference(ctx, w, p.tableOrganizations, "id", v.OrganizationID); err != nil {
		return err
	}
	if err := p.businessReference(ctx, w, p.tableGoals, "id", v.GoalID); err != nil {
		return err
	}
	return p.businessReference(ctx, w, p.tableAgents, "id", v.LeadAgentID)
}

func (p *Postgres) taskReferences(ctx context.Context, w *businessWrite, v service.Task) error {
	for _, ref := range []struct {
		table interface{}
		id    string
	}{{p.tableOrganizations, v.OrganizationID}, {p.tableProjects, v.ProjectID}, {p.tableGoals, v.GoalID}, {p.tableTasks, v.ParentID}, {p.tableAgents, v.AssignedAgentID}} {
		if err := p.businessReference(ctx, w, ref.table, "id", ref.id); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) approvalReferences(ctx context.Context, w *businessWrite, v service.Approval) error {
	if err := p.businessReference(ctx, w, p.tableOrganizations, "id", v.OrganizationID); err != nil {
		return err
	}
	if v.RequestedByType == "agent" {
		if err := p.businessReference(ctx, w, p.tableAgents, "id", v.RequestedByID); err != nil {
			return err
		}
	}
	for _, ref := range []struct {
		key   string
		table interface{}
	}{{"organization_id", p.tableOrganizations}, {"agent_id", p.tableAgents}, {"parent_agent_id", p.tableAgents}} {
		if raw, ok := v.RequestDetails[ref.key]; ok {
			value, ok := raw.(string)
			if !ok {
				return service.ErrAccessDenied
			}
			if err := p.businessReference(ctx, w, ref.table, "id", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Postgres) mcpReferences(ctx context.Context, w *businessWrite, c service.MCPServerConfig, sets []string) error {
	for _, name := range c.EnabledSkills {
		if err := p.businessNamedReference(ctx, w, p.tableSkills, "name", name); err != nil {
			return err
		}
	}
	for _, id := range c.WorkflowIDs {
		if err := p.businessReference(ctx, w, p.tableWorkflows, "id", id); err != nil {
			return err
		}
	}
	for _, name := range sets {
		if err := p.businessNamedReference(ctx, w, p.tableMCPSets, "name", name); err != nil {
			return err
		}
	}
	return nil
}

func mcpReadDTO(a service.AccessPrincipal, id, workspace string, c *service.MCPServerConfig, urls *[]string) {
	if a.Allows("credentials.manage", service.AccessResource{WorkspaceID: workspace, ID: id}) {
		return
	}
	for i := range c.HTTPTools {
		c.HTTPTools[i].URL = ""
		c.HTTPTools[i].Headers = nil
		c.HTTPTools[i].BodyTemplate = ""
	}
	c.MCPUpstreams = nil
	c.WSUpstream = nil
	*urls = nil
}

func (p *Postgres) botReferences(ctx context.Context, w *businessWrite, c service.BotConfig) error {
	if c.UserContainers && !w.actor.PlatformAdmin {
		return service.ErrAccessDenied
	}
	if err := p.businessReference(ctx, w, p.tableAgents, "id", c.DefaultAgentID); err != nil {
		return err
	}
	for _, id := range c.ChannelAgents {
		if err := p.businessReference(ctx, w, p.tableAgents, "id", id); err != nil {
			return err
		}
	}
	for _, id := range c.AllowedAgentIDs {
		if err := p.businessReference(ctx, w, p.tableAgents, "id", id); err != nil {
			return err
		}
	}
	for _, cmd := range c.CustomCommands {
		if err := p.businessReference(ctx, w, p.tableAgents, "id", cmd.AgentID); err != nil {
			return err
		}
		if err := p.businessReference(ctx, w, p.tableOrganizations, "id", cmd.OrganizationID); err != nil {
			return err
		}
	}
	return nil
}
