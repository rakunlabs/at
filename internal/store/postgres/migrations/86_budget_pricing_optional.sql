-- Model pricing enforcement on provider budgets becomes opt-in. Unpriced
-- models (notably System 1 decision services, which report no price) were
-- refused with provider_pricing_required by default; new policies now let
-- them through unless "Require model pricing" is ticked. Existing policies
-- keep the value they were saved with.
ALTER TABLE ${TABLE_PREFIX}provider_budget_policies
    ALTER COLUMN enforce_unpriced SET DEFAULT FALSE;
