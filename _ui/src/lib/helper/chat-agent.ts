import type { AgentConfig } from '../api/agents';

export interface ChatSelections {
  mcp_sets: string[];
  skills: string[];
  builtin_tools: string[];
}

/** Agent contributions never mutate the user's explicit selections. */
export function agentSelections(config?: AgentConfig): ChatSelections {
  return {
    mcp_sets: [...new Set(config?.mcp_sets ?? [])],
    skills: [...new Set((config?.skills ?? []).map(skill => typeof skill === 'string' ? skill : skill.id).filter(Boolean))],
    builtin_tools: [...new Set(config?.builtin_tools ?? [])],
  };
}

export function mergeChatSelections(user: ChatSelections, agent: ChatSelections): ChatSelections {
  return {
    mcp_sets: [...new Set([...user.mcp_sets, ...agent.mcp_sets])],
    skills: [...new Set([...user.skills, ...agent.skills])],
    builtin_tools: [...new Set([...user.builtin_tools, ...agent.builtin_tools])],
  };
}
