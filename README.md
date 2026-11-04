# acse-index

`acse-index` is a small, open filter for your Terraform apply job. It reads
`terraform show -json` (the state, after apply) and writes a **resource index**:
for each managed resource, where it lives in your code and which cloud object
it is. It never writes an attribute value.

```sh
terraform show -json | acse-index --repo "$REPO" --dir "$DIR" --commit "$SHA" --run "$RUN_REF" > index.json
# then copy index.json to <prefix>/<repo>/<dir>/<commit>.json in the bucket prefix ACSE reads
```

ACSE uses the index to find the code behind a cloud resource. The index stays
in your bucket.

## What it keeps

The header holds `repository`, `working_directory`, `commit`, `run_ref` and
`produced_at`, plus the producer (`acse-index` and its version). Each managed
resource gets these fields, and nothing else:

| Field | From |
|---|---|
| `address` | the resource's full address, e.g. `module.net.aws_vpc.this` |
| `type` | the resource type |
| `provider` | the provider source address, e.g. `registry.terraform.io/hashicorp/aws` |
| `module_path` | the module the resource sits in. Absent at the root |
| `cloud_id` | only for `hashicorp/aws`, `google`, `google-beta`, `azurerm` and `azuread` (see below) |

The filter also:
- drops every attribute value, output, variable and provider configuration;
- drops data sources;
- refuses a plan (`planned_values`), so run it on the state after apply.

### How it picks `cloud_id`

The filter checks a fixed list of attributes, in order:

| Provider | Order |
|---|---|
| `aws` | `arn`, then `id` |
| `google`, `google-beta` | `self_link`, then `id` |
| `azurerm`, `azuread` | `id` |

It keeps the first attribute that is a non-empty, printable string and is not
marked sensitive. Resources from any other provider (`random`, `tls`, `null`,
`kubernetes`, and so on) keep no id. Some of those ids are the secret itself:
`random_string`'s `id` is its generated value.

## Two forms, one output

| Form | Files | Needs |
|---|---|---|
| Static binary | `acse-index-{linux,darwin}-{amd64,arm64}` | nothing |
| Script | `acse-index.sh` and `acse-index.jq`, kept side by side | `jq` 1.6 or later |

Both take the same flags. On the shared fixtures in `testdata/` they produce
byte-identical output, and CI checks this on every change.

If something is wrong, the filter exits non-zero and writes nothing to stdout:
- a flag the schema would refuse;
- input that is not a `terraform show -json` state;
- more than 50,000 resources in one root.

`--produced-at` sets the recorded time. It defaults to the current time in UTC.

## Verifying a release

Every release carries:
- `SHA256SUMS` for every asset;
- a GitHub artifact attestation (build provenance) for every asset.

```sh
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify acse-index-linux-amd64 --repo initvalueio/acse-index
```

You can also run your own build on the AC-3 fixture and confirm that none of
its secret values appear in the output:

```sh
acse-index --repo o/r --dir . --commit $(printf '0%.0s' {1..40}) --run r1 \
  < testdata/fixtures/ac3-secrets.json \
  | grep -F -f <(grep -v '^#' testdata/fixtures/ac3-secrets.secrets) && echo LEAK || echo clean
```

The fixture holds `random_string`, `random_password`, `tls_private_key`,
`aws_secretsmanager_secret_version`, and a sensitive output.

## The schema

`schema/resource-index-1.0.schema.json` is ACSE's contract for what it reads:
- every object is strict (`additionalProperties: false`);
- data sources are refused;
- `cloud_id` is refused for any provider not listed above.

The file is mirrored byte for byte from ACSE's contracts. `schema/PIN` records
the upstream commit and sha256, and a test fails if the mirror drifts.

You may audit this filter or replace it with your own. ACSE validates every
index against the schema, so a replacement cannot add values.

## Development

```sh
go test ./...                        # Go tests, golden files, AC-3, schema pin
go test ./... -update                # rewrite testdata/golden from the Go form
go build -o acse-index . && test/parity.sh ./acse-index   # Go vs jq parity
python3 test/validate_schema.py      # goldens against the schema (needs jsonschema)
```

When you change behaviour:
1. Change both forms.
2. Regenerate the goldens.
3. Confirm `test/parity.sh` passes.

## License

Apache-2.0. See [LICENSE](LICENSE).
