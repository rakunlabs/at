package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// maxRosterSkills caps how many skill names we list per agent in a roster
// or delegate-tool description, keeping the system prompt bounded when an
// agent is loaded with many skills.
const maxRosterSkills = 8

// resolveAgentOrgID finds the organization an agent belongs to: explicit
// memberships first, then a scan of all organizations for the agent as head.
// Returns "" when the agent has no organization (or agentID is empty).
// Best-effort: store errors resolve to "".
func (s *Server) resolveAgentOrgID(ctx context.Context, agentID string) string {
	if agentID == "" || s.orgAgentStore == nil || s.organizationStore == nil {
		return ""
	}
	if memberships, err := s.orgAgentStore.ListAgentOrganizations(ctx, agentID); err == nil && len(memberships) > 0 {
		return memberships[0].OrganizationID
	}
	if allOrgs, err := s.organizationStore.ListOrganizations(ctx, nil); err == nil {
		for _, o := range allOrgs.Data {
			if o.HeadAgentID == agentID {
				return o.ID
			}
		}
	}
	return ""
}

// resolveSkillNames turns an agent's SkillRefs into human-readable skill
// names. Each ref is resolved through the skill store (by ID first, then by
// name); unresolved refs fall back to the raw identifier so the caller
// always gets a usable label. Best-effort: store/lookup errors are skipped.
func (s *Server) resolveSkillNames(ctx context.Context, refs []service.SkillRef) []string {
	if len(refs) == 0 {
		return nil
	}
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		label := ref.ID
		if s.skillStore != nil {
			if sk, err := s.skillStore.GetSkill(ctx, ref.ID); err == nil && sk != nil && sk.Name != "" {
				label = sk.Name
			} else if sk, err := s.skillStore.GetSkillByName(ctx, ref.ID); err == nil && sk != nil && sk.Name != "" {
				label = sk.Name
			}
		}
		names = append(names, label)
	}
	return names
}

// agentCapabilitySummary produces a compact, single-line description of
// what an agent can do — its skills, builtin tools, and MCP tool sets — so
// a delegating manager can pick the right teammate and craft an
// appropriate instruction. Returns "" when the agent has no declared
// capabilities. Example:
//
//	skills: Video Composer, FFmpeg Guide · tools: bash_execute, task_create · mcp: elevenlabs
func (s *Server) agentCapabilitySummary(ctx context.Context, agent *service.Agent) string {
	if agent == nil {
		return ""
	}

	var parts []string

	if names := s.resolveSkillNames(ctx, agent.Config.Skills); len(names) > 0 {
		parts = append(parts, "skills: "+joinCapped(names, maxRosterSkills))
	}
	if len(agent.Config.BuiltinTools) > 0 {
		parts = append(parts, "tools: "+joinCapped(agent.Config.BuiltinTools, maxRosterSkills))
	}
	if len(agent.Config.MCPSets) > 0 {
		parts = append(parts, "mcp: "+joinCapped(agent.Config.MCPSets, maxRosterSkills))
	}
	if len(agent.Config.Workflows) > 0 {
		parts = append(parts, "workflows: "+joinCapped(agent.Config.Workflows, maxRosterSkills))
	}

	return strings.Join(parts, " · ")
}

// joinCapped joins up to n items with ", " and appends "+K more" when the
// slice is longer, so long capability lists stay bounded in the prompt.
func joinCapped(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", +%d more", len(items)-n)
}

// orgContextPrompt returns a system-prompt block introducing the
// organization the agent belongs to, so every agent shares a common frame
// (org name + mission) instead of operating blind. The head agent (depth 0)
// additionally gets an "owner of the outcome" framing. Returns "" when
// there is nothing meaningful to say.
func orgContextPrompt(org *service.Organization, depth int) string {
	if org == nil || org.Name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Organization\n")
	b.WriteString(fmt.Sprintf("You are an agent in **%s**.", org.Name))
	if org.Description != "" {
		b.WriteString(" " + strings.TrimSpace(org.Description))
	}
	if depth == 0 {
		b.WriteString("\nYou are the entry-point (head) agent for this task: you own the final outcome and coordinate the team below to achieve it.")
	}
	b.WriteString("\n")
	return b.String()
}

// delegationContextPrompt returns a system-prompt block telling a delegated
// (child) agent WHO asked for the work and WHY — the delegating agent's
// identity and the parent task it belongs to. Without this the child sees
// only a free-text instruction with no idea who it reports back to or how
// its work fits the bigger picture. Returns "" for root tasks or when the
// parent/delegator cannot be resolved. Best-effort: lookup failures degrade
// gracefully to a partial (or empty) block.
func (s *Server) delegationContextPrompt(ctx context.Context, org *service.Organization, task *service.Task) string {
	if task == nil || task.ParentID == "" || s.taskStore == nil {
		return ""
	}

	parent, err := s.taskStore.GetTask(ctx, task.ParentID)
	if err != nil || parent == nil {
		return ""
	}

	delegatorName := ""
	delegatorRole := ""
	if parent.AssignedAgentID != "" && s.agentStore != nil {
		if da, err := s.agentStore.GetAgent(ctx, parent.AssignedAgentID); err == nil && da != nil {
			delegatorName = da.Name
		}
		if org != nil && s.orgAgentStore != nil {
			if oa, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, parent.AssignedAgentID); err == nil && oa != nil {
				delegatorRole = strings.TrimSpace(strings.Trim(fmt.Sprintf("%s %s", oa.Role, oa.Title), " "))
			}
		}
	}
	if delegatorName == "" {
		delegatorName = "your manager"
	}

	parentLabel := parent.ID
	if parent.Identifier != "" {
		parentLabel = parent.Identifier
	}

	var b strings.Builder
	b.WriteString("\n\n## Delegation Context\n")
	if delegatorRole != "" {
		b.WriteString(fmt.Sprintf("You were delegated this task by **%s** (%s) as part of a larger effort.\n", delegatorName, delegatorRole))
	} else {
		b.WriteString(fmt.Sprintf("You were delegated this task by **%s** as part of a larger effort.\n", delegatorName))
	}
	b.WriteString(fmt.Sprintf("- Parent task %s: %q\n", parentLabel, parent.Title))
	b.WriteString("- Your specific assignment is the instruction in the user message above. Stay focused on it; do not try to own the whole parent task.\n")
	b.WriteString(fmt.Sprintf("- Report a clear, self-contained result: %s will review it and integrate it. If something is ambiguous and you cannot resolve it, state your assumptions explicitly in your result rather than stalling.\n", delegatorName))
	return b.String()
}
