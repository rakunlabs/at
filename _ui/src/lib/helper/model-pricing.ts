// Form state for the provider editor's per-model prices. Fields are strings so
// an empty input means "no price" rather than zero; zero is an explicit free price.

export interface ModelPriceForm {
  input: string;
  output: string;
  cache_read: string;
  cache_write: string;
}

export interface ModelPrice {
  input: number;
  output: number;
  cache_read?: number;
  cache_write?: number;
}

export const priceFields = ['input', 'output', 'cache_read', 'cache_write'] as const;

const emptyForm = (): ModelPriceForm => ({ input: '', output: '', cache_read: '', cache_write: '' });

export function priceFormFromConfig(pricing: Record<string, ModelPrice> | undefined): Record<string, ModelPriceForm> {
  const out: Record<string, ModelPriceForm> = {};
  for (const [model, price] of Object.entries(pricing || {})) {
    out[model] = {
      input: String(price.input ?? 0),
      output: String(price.output ?? 0),
      cache_read: price.cache_read ? String(price.cache_read) : '',
      cache_write: price.cache_write ? String(price.cache_write) : '',
    };
  }
  return out;
}

function filled(form: ModelPriceForm | undefined): form is ModelPriceForm {
  return !!form && priceFields.some((field) => form[field].trim() !== '');
}

/** First problem in the form, or '' when every filled row is valid. */
export function validatePriceForm(models: string[], form: Record<string, ModelPriceForm>): string {
  for (const model of models) {
    const row = form[model];
    if (!filled(row)) continue;
    if (row.input.trim() === '' || row.output.trim() === '') {
      return `Set both input and output prices for "${model}" (use 0 for free)`;
    }
    for (const field of priceFields) {
      const raw = row[field].trim();
      if (raw === '') continue;
      const value = Number(raw);
      if (!Number.isFinite(value) || value < 0) {
        return `Prices for "${model}" must be non-negative numbers`;
      }
    }
  }
  return '';
}

/** Config value for the filled rows of the listed models; undefined when none. */
export function priceConfigFromForm(models: string[], form: Record<string, ModelPriceForm>): Record<string, ModelPrice> | undefined {
  const out: Record<string, ModelPrice> = {};
  for (const model of models) {
    const row = form[model];
    if (!filled(row)) continue;
    const price: ModelPrice = { input: Number(row.input) || 0, output: Number(row.output) || 0 };
    const cacheRead = Number(row.cache_read);
    const cacheWrite = Number(row.cache_write);
    if (row.cache_read.trim() !== '' && cacheRead > 0) price.cache_read = cacheRead;
    if (row.cache_write.trim() !== '' && cacheWrite > 0) price.cache_write = cacheWrite;
    out[model] = price;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

export function setPriceField(form: Record<string, ModelPriceForm>, model: string, field: keyof ModelPriceForm, value: string): Record<string, ModelPriceForm> {
  return { ...form, [model]: { ...(form[model] || emptyForm()), [field]: value } };
}
