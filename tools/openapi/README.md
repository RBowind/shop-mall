# OpenAPI Contract Tooling

`docs/api/openapi.yaml` is the only source for endpoint shapes, response codes,
security requirements, and generated TypeScript types.

## Commands

Run from the repository root:

```sh
pnpm --filter @shop-mall/openapi generate
make contract-check
```

`generate` writes type-only outputs to:

- `miniapp/src/services/generated/api.d.ts`
- `admin/src/services/generated/api.d.ts`

The files are generated artifacts. They do not contain a runtime HTTP client.
Each application supplies its own transport adapter at the later implementation
boundary, so authentication, cookies, CSRF, trace propagation, and platform
request behavior remain outside generated code.

`contract-check` runs Redocly lint for YAML structure and `$ref` resolution, then
generates into temporary directories and compares the result byte-for-byte with
the checked-in artifacts. It does not contact a deployment URL and does not read
secrets.

All public int64 values use decimal strings in the contract. This avoids loss of
precision in JavaScript clients.

Each public int64 schema is marked with `x-public-int64: true`. The generator
parses the YAML document and checks a fixed set of public field pointers, requiring
`type: string`, a non-negative decimal pattern (`^[0-9]+$` or the stricter
positive form `^[1-9][0-9]*$`), and a string example. Numeric `number`/`integer`
types and OpenAPI `format: int64` are rejected. The signed ledger `delta` fields
also carry `x-public-int64-signed: true` and use a signed decimal pattern because
the points ledger permits both positive and negative adjustments.
