// Copyright 2026 The acse-index Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the Go implementation")

// The arguments every shared fixture is run with, here and in test/parity.sh.
var fixtureArgs = []string{
	"--repo", "example-org/platform-infra",
	"--dir", "envs/prod",
	"--commit", "9c1e5b7a3d2f4e6081a9b8c7d6e5f4a3b2c1d0e9",
	"--run", "run-AbCdEf123456",
	"--produced-at", "2026-11-04T16:00:00Z",
}

func fixedNow() time.Time { return time.Date(2026, 11, 4, 16, 0, 0, 0, time.UTC) }

func runFile(t *testing.T, path string, args []string) (string, string, int) {
	t.Helper()
	in, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run(args, bytes.NewReader(in), &out, &errb, fixedNow)
	return out.String(), errb.String(), code
}

func fixtures(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/fixtures/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return files
}

func TestGolden(t *testing.T) {
	for _, f := range fixtures(t) {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			out, stderr, code := runFile(t, f, fixtureArgs)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			golden := filepath.Join("testdata/golden", name)
			if *update {
				if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if out != string(want) {
				t.Errorf("output differs from %s\n--- got\n%s\n--- want\n%s", golden, out, want)
			}
		})
	}
}

// The output carries exactly the schema's fields, nothing else.
func TestOnlyIndexFieldsAreWritten(t *testing.T) {
	allowedTop := []string{"commit", "produced_at", "producer", "repository", "resource_index_format_version", "resources", "run_ref", "working_directory"}
	allowedRes := map[string]bool{"address": true, "type": true, "provider": true, "module_path": true, "cloud_id": true}
	for _, f := range fixtures(t) {
		out, _, code := runFile(t, f, fixtureArgs)
		if code != 0 {
			t.Fatalf("%s: exit %d", f, code)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var keys []string
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(allowedTop, ",") {
			t.Errorf("%s: top-level keys %v", f, keys)
		}
		var res []map[string]any
		if err := json.Unmarshal(doc["resources"], &res); err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			for k, v := range r {
				if !allowedRes[k] {
					t.Errorf("%s: resource key %q", f, k)
				}
				if _, ok := v.(string); !ok {
					t.Errorf("%s: %s is not a string", f, k)
				}
			}
		}
	}
}

// AC-3: a fixture with secrets in state yields an index with none of them.
func TestAC3SecretsFixtureLeaksNothing(t *testing.T) {
	out, stderr, code := runFile(t, "testdata/fixtures/ac3-secrets.json", fixtureArgs)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	raw, err := os.ReadFile("testdata/fixtures/ac3-secrets.secrets")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		if strings.Contains(out, line) {
			t.Errorf("secret value %q appears in the index", line)
		}
	}
	if n < 10 {
		t.Fatalf("secrets list too short (%d)", n)
	}
	// Every utility resource is still indexed, with no id.
	for _, addr := range []string{"random_string.suffix", "random_password.db", "tls_private_key.deploy", "aws_secretsmanager_secret_version.db", "module.app.random_password.api_key", "module.app.random_string.token"} {
		if !strings.Contains(out, `"address": "`+addr+`"`) {
			t.Errorf("%s missing from the index", addr)
		}
	}
}

// For every provider ACSE does not scan, no string the state holds for the
// resource may reach the index other than its address, type and provider.
func TestNonCloudResourcesKeepNoValue(t *testing.T) {
	for _, f := range fixtures(t) {
		in, _ := os.ReadFile(f)
		var doc any
		if err := json.Unmarshal(in, &doc); err != nil {
			t.Fatal(err)
		}
		out, _, _ := runFile(t, f, fixtureArgs)
		visitResources(doc, func(r map[string]any) {
			if reCloud.MatchString(r["provider_name"].(string)) {
				return
			}
			for _, s := range stringLeaves(r["values"]) {
				if len(s) < 4 || strings.Contains(r["address"].(string), s) || s == r["type"] {
					continue
				}
				if strings.Contains(out, s) {
					t.Errorf("%s: value %q of %s appears in the index", f, s, r["address"])
				}
			}
		})
	}
}

func visitResources(doc any, fn func(map[string]any)) {
	var walkMod func(m map[string]any)
	walkMod = func(m map[string]any) {
		if rs, ok := m["resources"].([]any); ok {
			for _, r := range rs {
				fn(r.(map[string]any))
			}
		}
		if cs, ok := m["child_modules"].([]any); ok {
			for _, c := range cs {
				walkMod(c.(map[string]any))
			}
		}
	}
	if top, ok := doc.(map[string]any); ok {
		if v, ok := top["values"].(map[string]any); ok {
			if root, ok := v["root_module"].(map[string]any); ok {
				walkMod(root)
			}
		}
	}
}

func stringLeaves(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, stringLeaves(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, stringLeaves(e)...)
		}
		return out
	}
	return nil
}

func TestCloudIDRule(t *testing.T) {
	out, _, code := runFile(t, "testdata/fixtures/multi-cloud.json", fixtureArgs)
	if code != 0 {
		t.Fatal(code)
	}
	var doc struct {
		Resources []map[string]string `json:"resources"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range doc.Resources {
		got[r["address"]] = r["cloud_id"]
	}
	want := map[string]string{
		"aws_s3_bucket.logs":            "arn:aws:s3:::example-logs-7f3a",                         // arn first
		"aws_iam_role.ci":               "ci-deployer",                                            // arn null: id
		"aws_db_instance.main":          "db-EXAMPLEID",                                           // arn marked sensitive: id
		"google_storage_bucket.assets":  "https://www.googleapis.com/storage/v1/b/example-assets", // self_link first
		"google_compute_network.vpc":    "projects/example-project/global/networks/vpc",           // google-beta, empty self_link: id
		"azurerm_resource_group.rg":     "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-example",
		"azuread_application.app":       "/applications/11111111-1111-1111-1111-111111111111",
		"kubernetes_namespace.apps":     "",
		"null_resource.wait":            "",
		"cloudflare_record.www":         "",
		"aws_sqs_queue.jobs":            "arn:aws:sqs:eu-west-1:123456789012:jobs",
		"module.data.aws_s3_bucket.raw": "arn:aws:s3:::example-raw", // a module named data is not a data source
		"module.network.aws_vpc.this":   "arn:aws:ec2:eu-west-1:123456789012:vpc/vpc-0abc",
	}
	for addr, id := range want {
		v, ok := got[addr]
		if !ok {
			t.Errorf("%s missing", addr)
			continue
		}
		if v != id {
			t.Errorf("%s: cloud_id %q, want %q", addr, v, id)
		}
	}
	for addr := range got {
		if reDataAddr.MatchString(addr) {
			t.Errorf("data source %s indexed", addr)
		}
	}
}

func TestInputErrors(t *testing.T) {
	files, _ := filepath.Glob("testdata/errors/*.json")
	if len(files) == 0 {
		t.Fatal("no error fixtures")
	}
	for _, f := range files {
		out, stderr, code := runFile(t, f, fixtureArgs)
		if code == 0 || out != "" {
			t.Errorf("%s: exit %d, stdout %q", f, code, out)
		}
		if !strings.HasPrefix(stderr, "acse-index: ") {
			t.Errorf("%s: stderr %q", f, stderr)
		}
	}
}

// argCases are shared with test/parity.sh through testdata/arg-errors.txt.
func TestArgumentErrors(t *testing.T) {
	raw, err := os.ReadFile("testdata/arg-errors.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args := strings.Split(line, "\t")
		out, _, code := runFile(t, "testdata/fixtures/empty-state.json", args)
		if code == 0 || out != "" {
			t.Errorf("%q: exit %d, stdout %q", args, code, out)
		}
	}
}

func TestDefaultProducedAt(t *testing.T) {
	out, _, code := runFile(t, "testdata/fixtures/empty-state.json", fixtureArgs[:8])
	if code != 0 || !strings.Contains(out, `"produced_at": "2026-11-04T16:00:00Z"`) {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestDotDirectory(t *testing.T) {
	args := append([]string{}, fixtureArgs...)
	args[3] = "."
	if _, stderr, code := runFile(t, "testdata/fixtures/empty-state.json", args); code != 0 {
		t.Fatal(stderr)
	}
}

// The jq script and the binary must report the same producer version.
func TestVersionMatchesScript(t *testing.T) {
	src, err := os.ReadFile("script/acse-index.jq")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^def acse_index_version: "([^"]+)";$`).FindSubmatch(src)
	if m == nil || string(m[1]) != version {
		t.Fatalf("script version %q, binary version %q", m, version)
	}
}

// The schema is mirrored from ACSE's contracts; it must stay byte-identical
// to the pinned upstream file. Updating it means updating PIN in the same
// change, from the upstream commit named there.
func TestSchemaPin(t *testing.T) {
	pin, err := os.ReadFile("schema/PIN")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(pin), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if !regexp.MustCompile(`\A[0-9a-f]{40}\z`).MatchString(fields["upstream_commit"]) {
		t.Fatalf("PIN upstream_commit %q", fields["upstream_commit"])
	}
	schema, err := os.ReadFile(filepath.Join("schema", fields["file"]))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(schema)
	if got := hex.EncodeToString(sum[:]); got != fields["sha256"] {
		t.Fatalf("schema sha256 %s, pinned %s", got, fields["sha256"])
	}
	var s struct {
		Properties struct {
			Version struct {
				Const string `json:"const"`
			} `json:"resource_index_format_version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil || s.Properties.Version.Const != FormatVersion {
		t.Fatalf("schema format version %q, filter writes %q", s.Properties.Version.Const, FormatVersion)
	}
}
