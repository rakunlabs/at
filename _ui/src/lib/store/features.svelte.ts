import {
  listFeatures,
  type Feature,
  type FeatureGroup,
  type FeaturePreset,
  type FeaturesResponse,
} from '@/lib/api/features';

export const storeFeatures = $state({
  loaded: false,
  loading: false,
  groups: [] as FeatureGroup[],
  features: [] as Feature[],
  presets: [] as FeaturePreset[],
  flags: {} as Record<string, boolean>,
});

let loadPromise: Promise<void> | null = null;

/**
 * Reads the *effective* state, which is what the backend gate applies: a child
 * whose parent is off is unavailable even though its own switch reads on.
 * Before the catalog loads everything reads as enabled, so a slow or failed
 * request never hides the whole application.
 */
export function isFeatureEnabled(key: string): boolean {
  if (!storeFeatures.loaded) return true;
  return storeFeatures.flags[key] !== false;
}

function indexFlags() {
  const flags: Record<string, boolean> = {};
  for (const feature of storeFeatures.features) {
    flags[feature.key] = feature.effective;
  }
  storeFeatures.flags = flags;
}

/**
 * Replaces the whole catalog. Toggling a parent changes what every descendant
 * resolves to, so a write answers with the full state rather than one row.
 */
export function applyFeatures(res: FeaturesResponse) {
  storeFeatures.groups = res.groups || [];
  storeFeatures.features = res.features || [];
  storeFeatures.presets = res.presets || [];
  storeFeatures.loaded = true;
  indexFlags();
}

export function applyFeature(feature: Feature) {
  storeFeatures.features = storeFeatures.features.map((item) => (item.key === feature.key ? feature : item));
  storeFeatures.groups = storeFeatures.groups.map((group) => ({
    ...group,
    features: group.features.map((item) => (item.key === feature.key ? feature : item)),
  }));
  indexFlags();
}

export async function loadFeatures(force = false): Promise<void> {
  if (storeFeatures.loaded && !force) return;
  if (loadPromise && !force) return loadPromise;

  storeFeatures.loading = true;
  loadPromise = listFeatures()
    .then(applyFeatures)
    .finally(() => {
      storeFeatures.loading = false;
      loadPromise = null;
    });

  return loadPromise;
}
