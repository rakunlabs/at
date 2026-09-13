# AT Pricing

Reviewed provider prices maintained alongside AT. Edit `providers/<provider>.json`;
`index.json` is the generated public catalog consumed by **Model Pricing → AT Pricing**:

https://raw.githubusercontent.com/rakunlabs/at/main/pricing/index.json

## Update prices

1. Check the provider's **official** pricing and model documentation.
2. Edit or add a provider file. Every model needs a source URL, verification date
   (`YYYY-MM-DD`) and notes describing the rate's scope.
3. From the repository root, run:

   ```sh
   go run ./pricing/cmd/catalog
   go test ./pricing/...
   ```

4. Include both the provider file and generated `index.json` in the PR.
   CI checks the schema, duplicate IDs/aliases, prices and generated-index freshness.
5. Once merged into `main`, deployed AT instances can fetch the new catalog without
   a server rebuild. A local change alone does not publish it to GitHub.

## Contract (v1)

- Currency: **USD**. All four prices are **per 1 million tokens**.
- `input`, `output`, `cache_read`, `cache_write` are explicit numbers or `null`.
  `null` means unknown; such entries are **not offered for sync**, because the
  effective-price database only supports numbers. Never invent a zero for missing data.
- `0` means no charge in that billing bucket (including an unsupported operation
  or writes already billed as ordinary input). Explain this in `notes`.
- Aliases must be verified API identifiers for the **same rate**. They map back
  to the canonical model, so saved mappings survive an alias change.
- Providers name the **billing vendor**, not the adapter protocol. An
  OpenAI-compatible connection can use a unique exact model/alias match from
  another vendor. Cloud adapters are not automatically assigned direct API rates.
  Azure/Bedrock/Vertex pricing should have their own verified provider files.
- Standard global, synchronous text-token rates are the default scope. These
  are API list prices, not subscription bills. Media, tool calls, taxes, discounts,
  batch/fast modes and residency uplifts are not included unless explicitly noted.

### Conditional rates

Use separate IDs such as `grok-4.6@short` / `grok-4.6@long` or
`deepseek-flash@peak` / `deepseek-flash@off-peak`, with `manual_only: true` and
precise scope notes. These are **catalog profile IDs, not upstream model IDs**.
They are available in the price-reference selector but never matched automatically.
The user can explicitly map a workload to a profile; it remains a **fixed rate**.
AT does not dynamically switch tiers by context, time, region or cache duration.
Use a dedicated provider/model mapping for workloads with a known profile.

Anthropic's default entries use 5-minute cache writes. A 1-hour cache configuration
needs a separate manually selected profile or a manual price override.

## Sync behavior

The server fetches and validates the entire index with a bounded request before
building a preview. Apply fetches again, uses authoritative catalog rates and keeps
manual overrides unless explicitly overwritten. Unknown, invalid or unavailable
catalogs never silently fall back to zero prices or replace database records.
`llm-prices` remains selectable for older saved mappings.

Initial coverage: OpenAI, Anthropic, MiniMax, DeepSeek, xAI and a small verified
Cohere subset. This is a curated starting catalog, not an assertion of complete
provider coverage. Google AI pricing could not be retrieved during initial
verification, so no unverified Gemini prices are included.

`schema.json` describes editable provider files. The Go validator is also used
by the remote reader; the CI schema check enforces required keys and JSON types.
