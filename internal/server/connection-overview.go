package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ─── Connection overview ───
//
// The Connections page answers "which accounts exist" but not "who needs one".
// This view joins the three places a credential requirement can come from —
// agent bindings, skills whose tools read <provider>_* variables, and global
// variables used as the installation-wide fallback — so the page can show,
// per provider, which agents run on which account and which agents silently
// fall back to the shared global credential.

type connectionOverviewAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// ConnectionID is the account the agent is bound to for this provider
	// (agent default or a skill override); empty means it falls back.
	ConnectionID string `json:"connection_id,omitempty"`
	// Level is "agent" or "skill" when bound.
	Level string `json:"level,omitempty"`
	// Skills names the attached skills whose tools need this provider.
	Skills []string `json:"skills,omitempty"`
	// Editable reports whether the caller may change this agent's binding.
	Editable bool `json:"editable"`
}

type connectionOverviewProvider struct {
	Provider string `json:"provider"`
	// Agents that need or bind this provider.
	Agents []connectionOverviewAgent `json:"agents"`
	// Skills (by name) whose tools read this provider's credentials.
	Skills []string `json:"skills,omitempty"`
	// GlobalFallback lists the installation variables a tool falls back to
	// when nothing is bound, with whether each one has a value.
	GlobalFallback []connectionOverviewVar `json:"global_fallback,omitempty"`
}

type connectionOverviewVar struct {
	Key string `json:"key"`
	Set bool   `json:"set"`
}

type connectionOverview struct {
	Providers []connectionOverviewProvider `json:"providers"`
}

// ConnectionOverviewAPI handles GET /api/v1/connections/overview.
func (s *Server) ConnectionOverviewAPI(w http.ResponseWriter, r *http.Request) {
	out, err := s.connectionOverview(r.Context())
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		slog.Error("connection overview failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to build connection overview: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, out, http.StatusOK)
}

func (s *Server) connectionOverview(ctx context.Context) (*connectionOverview, error) {
	byProvider := map[string]*connectionOverviewProvider{}
	get := func(provider string) *connectionOverviewProvider {
		p := byProvider[provider]
		if p == nil {
			p = &connectionOverviewProvider{Provider: provider}
			byProvider[provider] = p
		}
		return p
	}

	// Skills: the providers each skill's tools read.
	skillProviders := map[string][]string{} // skill id/name -> providers
	skillNames := map[string]string{}       // skill id/name -> display name
	if s.skillStore != nil {
		res, err := s.skillStore.ListSkills(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		if res != nil {
			for i := range res.Data {
				sk := &res.Data[i]
				providers := skillCredentialProviders(sk)
				if len(providers) == 0 {
					continue
				}
				for _, key := range []string{sk.ID, sk.Name} {
					if key != "" {
						skillProviders[key] = providers
						skillNames[key] = sk.Name
					}
				}
				for _, provider := range providers {
					p := get(provider)
					if !slices.Contains(p.Skills, sk.Name) {
						p.Skills = append(p.Skills, sk.Name)
					}
				}
			}
		}
	}

	principal, _ := service.AccessPrincipalFromContext(ctx)
	if s.agentStore != nil {
		res, err := s.agentStore.ListAgents(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("list agents: %w", err)
		}
		if res != nil {
			for _, a := range res.Data {
				needs := map[string][]string{} // provider -> skill names
				for _, ref := range a.Config.Skills {
					for _, provider := range skillProviders[ref.ID] {
						name := skillNames[ref.ID]
						if name == "" {
							name = ref.ID
						}
						needs[provider] = append(needs[provider], name)
					}
				}
				bound := map[string][2]string{} // provider -> {connection id, level}
				for provider, id := range a.Config.Connections {
					if id != "" {
						bound[provider] = [2]string{id, "agent"}
					}
				}
				for _, ref := range a.Config.Skills {
					for provider, id := range ref.Connections {
						if id != "" {
							bound[provider] = [2]string{id, "skill"}
						}
					}
				}
				providers := make([]string, 0, len(needs)+len(bound))
				for provider := range needs {
					providers = append(providers, provider)
				}
				for provider := range bound {
					if _, ok := needs[provider]; !ok {
						providers = append(providers, provider)
					}
				}
				scope := a.Scope
				if scope == "" {
					scope = service.DeriveAgentScope(a)
				}
				editable := agentBindingEditable(principal, a)
				for _, provider := range providers {
					entry := connectionOverviewAgent{
						ID: a.ID, Name: a.Name, Scope: scope, Skills: needs[provider], Editable: editable,
					}
					if b, ok := bound[provider]; ok {
						entry.ConnectionID, entry.Level = b[0], b[1]
					}
					get(provider).Agents = append(get(provider).Agents, entry)
				}
			}
		}
	}

	// Global fallback variables (presence only, never values).
	if s.variableStore != nil && len(byProvider) > 0 {
		res, err := s.variableStore.ListVariables(ctx, nil)
		if err == nil && res != nil {
			present := map[string]bool{}
			for _, v := range res.Data {
				present[v.Key] = v.Value != "" || v.Secret
			}
			for provider, p := range byProvider {
				for _, suffix := range []string{"_client_id", "_client_secret", "_refresh_token", "_api_key"} {
					key := provider + suffix
					if set, ok := present[key]; ok {
						p.GlobalFallback = append(p.GlobalFallback, connectionOverviewVar{Key: key, Set: set})
					}
				}
			}
		}
	}

	out := &connectionOverview{Providers: make([]connectionOverviewProvider, 0, len(byProvider))}
	for _, p := range byProvider {
		sort.Slice(p.Agents, func(i, j int) bool { return strings.ToLower(p.Agents[i].Name) < strings.ToLower(p.Agents[j].Name) })
		sort.Strings(p.Skills)
		out.Providers = append(out.Providers, *p)
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].Provider < out.Providers[j].Provider })
	return out, nil
}

// skillCredentialProviders returns the providers whose <provider>_* variables
// a skill's tools read, from the preserved documentation copy of its tool
// handlers (or legacy tools still present on the record).
func skillCredentialProviders(sk *service.Skill) []string {
	var text strings.Builder
	text.WriteString(sk.SystemPrompt)
	for _, tool := range sk.Tools {
		text.WriteString("\n")
		text.WriteString(tool.Handler)
	}
	var providers []string
	for _, key := range inlineToolVariableKeys(text.String()) {
		provider, _, ok := workflow.ResolveConnectionKey(key)
		if ok && !slices.Contains(providers, provider) {
			providers = append(providers, provider)
		}
	}
	sort.Strings(providers)
	return providers
}

// agentBindingEditable mirrors the store's agent write rule closely enough to
// decide whether to offer an edit control; the store remains the authority.
func agentBindingEditable(p service.AccessPrincipal, a service.Agent) bool {
	if p.UserID == "" {
		return false
	}
	if a.OwnerUserID != "" {
		return a.OwnerUserID == p.UserID || p.PlatformAdmin
	}
	if a.Config.SharedWithAllWorkspaces {
		return p.PlatformAdmin && p.WorkspaceID == service.DefaultWorkspaceID
	}
	return p.Allows("agents.write", service.AccessResource{WorkspaceID: p.WorkspaceID, ID: a.ID})
}

// ─── Bind a connection to agents ───

type connectionBindRequest struct {
	// Agents to bind. An empty connection_id on the path-level request
	// unbinds the provider instead (falls back to globals).
	AgentIDs []string `json:"agent_ids"`
	// Unbind removes the provider binding from the listed agents.
	Unbind bool `json:"unbind,omitempty"`
}

type connectionBindResult struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name,omitempty"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// BindConnectionAgentsAPI handles POST /api/v1/connections/{id}/agents. It sets
// (or with unbind removes) the agent-level binding for the connection's
// provider on each listed agent and reports per agent, so one agent the
// caller may not edit does not abort the others. The store re-validates every
// write: a workspace agent cannot carry a personal connection.
func (s *Server) BindConnectionAgentsAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil || s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	conn, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to load connection: %v", err), http.StatusInternalServerError)
		return
	}
	if conn == nil || conn.Provider == service.GitSSHCredentialProvider {
		httpResponse(w, fmt.Sprintf("connection %q not found", id), http.StatusNotFound)
		return
	}
	var req connectionBindRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.AgentIDs) == 0 || len(req.AgentIDs) > 100 {
		httpResponse(w, "agent_ids must list 1 to 100 agents", http.StatusBadRequest)
		return
	}
	results := make([]connectionBindResult, 0, len(req.AgentIDs))
	for _, agentID := range req.AgentIDs {
		result := connectionBindResult{AgentID: agentID}
		name, err := s.setAgentConnectionBinding(r.Context(), agentID, conn, req.Unbind, s.getUserEmail(r))
		result.Name = name
		if err != nil {
			result.Error = connectionBindError(err)
		} else {
			result.OK = true
		}
		results = append(results, result)
	}
	httpResponseJSON(w, map[string]any{"provider": conn.Provider, "connection_id": conn.ID, "results": results}, http.StatusOK)
}

func (s *Server) setAgentConnectionBinding(ctx context.Context, agentID string, conn *service.Connection, unbind bool, by string) (string, error) {
	agent, err := s.agentStore.GetAgent(ctx, agentID)
	if err != nil {
		return "", err
	}
	if agent == nil {
		return "", service.ErrAccessResourceNotFound
	}
	if agent.Config.Connections == nil {
		agent.Config.Connections = map[string]string{}
	}
	if unbind {
		if agent.Config.Connections[conn.Provider] != conn.ID {
			return agent.Name, nil
		}
		delete(agent.Config.Connections, conn.Provider)
	} else {
		if agent.OwnerUserID == "" && conn.OwnerUserID != "" {
			return agent.Name, errPersonalOnSharedAgent
		}
		agent.Config.Connections[conn.Provider] = conn.ID
	}
	agent.UpdatedBy = by
	updated, err := s.agentStore.UpdateAgent(ctx, agent.ID, *agent)
	if err != nil {
		return agent.Name, err
	}
	if updated == nil {
		return agent.Name, service.ErrAccessResourceNotFound
	}
	return agent.Name, nil
}

var errPersonalOnSharedAgent = errors.New("a workspace agent runs for everyone and cannot use a personal connection; create a workspace connection or use a personal agent")

func connectionBindError(err error) string {
	switch {
	case errors.Is(err, errPersonalOnSharedAgent):
		return err.Error()
	case errors.Is(err, service.ErrAccessResourceNotFound):
		return "agent or connection not found"
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrExecutionDenied):
		return "you cannot edit this agent"
	default:
		return err.Error()
	}
}
