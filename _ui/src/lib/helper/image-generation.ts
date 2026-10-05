import type { ImageGenerationConfig } from '@/lib/api/mcp-servers';

/** Editable form state; every field is a string so inputs can bind to it. */
export type ImageGenerationForm = Required<ImageGenerationConfig>;

export function imageGenerationForm(config?: ImageGenerationConfig): ImageGenerationForm {
  return {
    provider: config?.provider ?? '',
    model: config?.model ?? '',
    size: config?.size ?? '',
    quality: config?.quality ?? '',
    background: config?.background ?? '',
  };
}

/**
 * The stored config for a form, or undefined when generate_image is not
 * enabled or nothing is set — an empty object would only add noise.
 */
export function imageGenerationConfig(form: ImageGenerationForm, builtinTools: string[]): ImageGenerationConfig | undefined {
  if (!builtinTools.includes('generate_image')) return undefined;
  const out: ImageGenerationConfig = {};
  for (const [key, raw] of Object.entries(form) as [keyof ImageGenerationForm, string][]) {
    const value = raw.trim();
    if (value) out[key] = value;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}
