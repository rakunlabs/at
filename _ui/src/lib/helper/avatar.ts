// DiceBear Bottts avatar generator — deterministic robot avatars from a seed string.
// Generated locally via @dicebear/core + @dicebear/bottts — no network calls needed.

import { createAvatar } from '@dicebear/core';
import * as bottts from '@dicebear/bottts';

// Soft pastel backgrounds that work in both light and dark themes.
const BG_COLORS: string[] = ['b6e3f4', 'c0aede', 'd1d4f9', 'ffd5dc', 'ffdfbf'];

// Cache generated data URIs to avoid re-computing the same avatar repeatedly.
const cache = new Map<string, string>();

/**
 * Generate a DiceBear Bottts avatar as a data URI.
 * Same seed always produces the same robot.
 */
export function generateAvatar(seed: string, size = 64): string {
  const key = `${seed}:${size}`;
  let uri = cache.get(key);
  if (!uri) {
    uri = createAvatar(bottts, {
      seed,
      size,
      backgroundColor: BG_COLORS,
      randomizeIds: true,
    }).toDataUri();
    cache.set(key, uri);
  }
  return uri;
}

/**
 * Return the avatar to display for an agent.
 * Uses the custom avatar_seed if set, otherwise falls back to the agent name.
 */
export function agentAvatar(avatarSeed: string | undefined, name: string, size = 64): string {
  return generateAvatar(avatarSeed || name, size);
}
