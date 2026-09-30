// Reasoning-effort vocabulary shared by every model picker. Mirrors
// internal/service/reasoning.go; the server remains the authority.

export const REASONING_EFFORT_LEVELS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const;

/** Efforts an adapter can express (service.ProviderReasoningEfforts). */
export function providerReasoningEfforts(type: string): string[] {
  switch (type) {
    case 'openai':
    case 'azure':
    case 'vertex':
      return [...REASONING_EFFORT_LEVELS];
    case 'anthropic':
      return ['low', 'medium', 'high', 'xhigh', 'max'];
    case 'minimax':
      return ['low', 'medium', 'high'];
    case 'gemini':
    case 'vertex-gemini':
      return ['minimal', 'low', 'medium', 'high'];
    default:
      return [];
  }
}

/**
 * Efforts to offer for one model. `perModel` is the provider's
 * `reasoning_efforts` map from /api/v1/info: a listed model uses exactly its
 * entry (empty = non-reasoning), an unlisted one falls back to the adapter.
 * `known` tells the caller whether the list is model-specific.
 */
export function modelReasoningEfforts(
  type: string,
  model: string,
  perModel?: Record<string, string[]>,
): { efforts: string[]; known: boolean } {
  const listed = perModel?.[model];
  if (listed) return { efforts: [...listed], known: true };
  return { efforts: providerReasoningEfforts(type), known: false };
}
