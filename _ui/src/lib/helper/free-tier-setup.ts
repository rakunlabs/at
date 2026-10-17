import type { FreeTierDraft } from '@/lib/api/free-tier';
import type { LLMConfig } from '@/lib/api/providers';
import type { RoutingProfile } from '@/lib/api/routing-profiles';

export function canAdoptFreeTierProfile(profile: RoutingProfile, targets: string[]): boolean {
  return !!profile.owner_user_id && profile.strategy === 'priority' && JSON.stringify(profile.targets) === JSON.stringify(targets);
}

// A response from a proxy is not proof that a write did not commit upstream.
// Only a definite client/admission refusal permits another explicit attempt.
export function uncertainTokenCreation(status?: number): boolean {
  return !status || status >= 500 || status === 408;
}

export function freeTierModels(text: string): string[] {
  return [...new Set(text.split(/[\n,]/).map((s) => s.trim()).filter(Boolean))];
}

// Only models the account holder explicitly chose are saved. Catalog hints
// are not an allowlist of free entitlements, and pricing is never guessed.
export function freeTierConfig(draft: FreeTierDraft): LLMConfig {
  const models = freeTierModels(draft.models);
  if (!models.length) throw new Error(`Choose a model for ${draft.entry.name}.`);
  if (!draft.key.trim()) throw new Error(`Add your own API key for ${draft.entry.name}.`);
  if (draft.entry.id === 'openrouter' && models.some((m) => m !== 'openrouter/free' && !m.endsWith(':free'))) {
    throw new Error('OpenRouter free setup accepts only openrouter/free or models ending in :free.');
  }
  return { type: draft.entry.type, base_url: draft.entry.base_url, api_key: draft.key.trim(), model: models[0], models,
    free_tier: draft.entry.id, quota: draft.quota,
    rate_limit: draft.rpm ? { requests_per_minute: draft.rpm } : undefined };
}

export function freeTierTargets(drafts: FreeTierDraft[], fallback: string): string[] {
  const targets = drafts.flatMap((d) => freeTierModels(d.models).map((m) => `${d.created_reference}/${m}`));
  return [...new Set([...targets, ...(fallback.trim() ? [fallback.trim()] : [])])];
}
