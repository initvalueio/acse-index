// Copyright 2026 The acse-index Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// FormatVersion is the resource-index schema version this filter writes.
const FormatVersion = "1.0"

// ProducerName is the producer name written into every index.
const ProducerName = "acse-index"

// MaxResources is the schema's bound on resources per index object.
const MaxResources = 50000

// Header is the identity of one apply of one root, supplied by the caller.
type Header struct {
	Repository       string
	WorkingDirectory string
	Commit           string
	RunRef           string
	ProducedAt       string
}

// Resource is one managed resource as written to the index. Nothing else
// about a resource is ever kept: no attribute value, no output, no variable.
type Resource struct {
	Address    string
	Type       string
	Provider   string
	ModulePath string // empty at the root
	CloudID    string // empty when the provider is not a scanned cloud
}

// The patterns below are the mirrored schema's, written with \A and \z so the
// Go and jq implementations anchor identically.
var (
	reRepository = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._-]{0,99}(/[A-Za-z0-9][A-Za-z0-9._-]{0,99}){1,3}\z`)
	reCommit     = regexp.MustCompile(`\A([0-9a-f]{40}|[0-9a-f]{64})\z`)
	reRunRef     = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._:/@-]{0,255}\z`)
	reProducedAt = regexp.MustCompile(`\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\z`)
	reType       = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,127}\z`)
	reProvider   = regexp.MustCompile(`\A[A-Za-z0-9.-]{1,253}(/[A-Za-z0-9_.-]{1,64}){1,2}\z`)
	reDataAddr   = regexp.MustCompile(`\A(module\.[^.]+\.)*data\.`)
	reCloud      = regexp.MustCompile(`/hashicorp/(aws|google|google-beta|azurerm|azuread)\z`)
)

// cloudIDAttributes is the fixed attribute order per scanned cloud provider
// (the schema allows a cloud id only for these). The first attribute holding a usable, non-sensitive
// string becomes cloud_id. Every other provider keeps no id at all: a utility
// provider's id can be the secret itself (random_string.id is its result).
var cloudIDAttributes = map[string][]string{
	"aws":         {"arn", "id"},
	"google":      {"self_link", "id"},
	"google-beta": {"self_link", "id"},
	"azurerm":     {"id"},
	"azuread":     {"id"},
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

// printable reports whether s is 1..max code points with no control character.
func printable(s string, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= max && !hasControl(s)
}

// ValidateHeader refuses a header the schema would refuse, so a bad flag fails
// the customer's step loudly instead of writing an index ACSE cannot read.
func ValidateHeader(h Header) error {
	if !reRepository.MatchString(h.Repository) {
		return errors.New("--repo must be owner/name (letters, digits, . _ -; up to four segments)")
	}
	wd := h.WorkingDirectory
	if wd != "." {
		if !printable(wd, 512) || strings.HasPrefix(wd, "/") || strings.HasPrefix(wd, ".") {
			return errors.New("--dir must be . or a repository-relative path not starting with / or .")
		}
	}
	if !reCommit.MatchString(h.Commit) {
		return errors.New("--commit must be a full lowercase hex commit id (40 or 64 characters)")
	}
	if !reRunRef.MatchString(h.RunRef) {
		return errors.New("--run must start with a letter or digit and use only letters, digits and . _ : / @ -")
	}
	if !reProducedAt.MatchString(h.ProducedAt) {
		return errors.New("--produced-at must be YYYY-MM-DDTHH:MM:SSZ")
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", h.ProducedAt)
	if err != nil || t.UTC().Format("2006-01-02T15:04:05Z") != h.ProducedAt {
		return errors.New("--produced-at is not a valid UTC time")
	}
	return nil
}

// Parse reads exactly one `terraform show -json` state document and returns
// the managed resources, in Terraform's own order (root module first, then
// each child module depth first).
func Parse(r io.Reader) ([]Resource, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no input: pipe `terraform show -json` into acse-index")
		}
		return nil, errors.New("input is not JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("input holds more than one JSON document")
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("input is not a `terraform show -json` document")
	}
	if _, plan := top["planned_values"]; plan {
		return nil, errors.New("input is a plan; run `terraform show -json` on the state after apply")
	}
	if _, plan := top["resource_changes"]; plan {
		return nil, errors.New("input is a plan; run `terraform show -json` on the state after apply")
	}
	values, err := optObject(top, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return []Resource{}, nil
	}
	root, err := optObject(values, "root_module")
	if err != nil {
		return nil, err
	}
	out := []Resource{}
	if root == nil {
		return out, nil
	}
	if err := walk(root, true, &out); err != nil {
		return nil, err
	}
	if len(out) > MaxResources {
		return nil, fmt.Errorf("more than %d managed resources in one root", MaxResources)
	}
	return out, nil
}

func optObject(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	o, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", key)
	}
	return o, nil
}

func optArray(m map[string]any, key string) ([]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return a, nil
}

func walk(mod map[string]any, isRoot bool, out *[]Resource) error {
	modulePath := ""
	if !isRoot {
		addr, ok := mod["address"].(string)
		if !ok || !strings.HasPrefix(addr, "module.") || !printable(addr, 1022) || utf8.RuneCountInString(addr) < 8 {
			return errors.New("a child module has no valid module address")
		}
		modulePath = addr
	}
	resources, err := optArray(mod, "resources")
	if err != nil {
		return err
	}
	for _, rv := range resources {
		r, ok := rv.(map[string]any)
		if !ok {
			return errors.New("a resource is not an object")
		}
		mode, ok := r["mode"].(string)
		if !ok {
			return errors.New("a resource has no mode")
		}
		if mode == "data" {
			continue
		}
		if mode != "managed" {
			return errors.New("a resource has an unknown mode")
		}
		address, ok := r["address"].(string)
		if !ok || !printable(address, 1024) {
			return errors.New("a managed resource has no valid address")
		}
		if reDataAddr.MatchString(address) {
			// The schema refuses any address that reads as a data source.
			continue
		}
		typ, ok := r["type"].(string)
		if !ok || !reType.MatchString(typ) {
			return fmt.Errorf("%s: resource type is not valid", address)
		}
		provider, ok := r["provider_name"].(string)
		if !ok || !reProvider.MatchString(provider) {
			return fmt.Errorf("%s: provider_name is not a provider source address", address)
		}
		res := Resource{Address: address, Type: typ, Provider: provider, ModulePath: modulePath}
		res.CloudID = cloudID(r, provider)
		*out = append(*out, res)
	}
	children, err := optArray(mod, "child_modules")
	if err != nil {
		return err
	}
	for _, cv := range children {
		c, ok := cv.(map[string]any)
		if !ok {
			return errors.New("a child module is not an object")
		}
		if err := walk(c, false, out); err != nil {
			return err
		}
	}
	return nil
}

func cloudID(r map[string]any, provider string) string {
	m := reCloud.FindStringSubmatch(provider)
	if m == nil {
		return ""
	}
	values, _ := r["values"].(map[string]any)
	sensitive, _ := r["sensitive_values"].(map[string]any)
	for _, attr := range cloudIDAttributes[m[1]] {
		if sensitive != nil {
			if b, ok := sensitive[attr].(bool); ok && b {
				continue
			}
		}
		if values == nil {
			continue
		}
		if s, ok := values[attr].(string); ok && printable(s, 2048) {
			return s
		}
	}
	return ""
}

// Render writes the index exactly as `jq` pretty-prints it (two-space indent,
// insertion key order, jq's string escaping), so both forms are byte-identical.
func Render(h Header, version string, resources []Resource) []byte {
	var b bytes.Buffer
	kv := func(indent, key, value string, last bool) {
		b.WriteString(indent)
		writeString(&b, key)
		b.WriteString(": ")
		writeString(&b, value)
		if !last {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("{\n")
	kv("  ", "resource_index_format_version", FormatVersion, false)
	kv("  ", "repository", h.Repository, false)
	kv("  ", "working_directory", h.WorkingDirectory, false)
	kv("  ", "commit", h.Commit, false)
	kv("  ", "run_ref", h.RunRef, false)
	kv("  ", "produced_at", h.ProducedAt, false)
	b.WriteString("  \"producer\": {\n")
	kv("    ", "name", ProducerName, false)
	kv("    ", "version", version, true)
	b.WriteString("  },\n")
	if len(resources) == 0 {
		b.WriteString("  \"resources\": []\n}\n")
		return b.Bytes()
	}
	b.WriteString("  \"resources\": [\n")
	for i, r := range resources {
		b.WriteString("    {\n")
		type field struct{ k, v string }
		fields := []field{{"address", r.Address}, {"type", r.Type}, {"provider", r.Provider}}
		if r.ModulePath != "" {
			fields = append(fields, field{"module_path", r.ModulePath})
		}
		if r.CloudID != "" {
			fields = append(fields, field{"cloud_id", r.CloudID})
		}
		for j, f := range fields {
			kv("      ", f.k, f.v, j == len(fields)-1)
		}
		if i == len(resources)-1 {
			b.WriteString("    }\n")
		} else {
			b.WriteString("    },\n")
		}
	}
	b.WriteString("  ]\n}\n")
	return b.Bytes()
}

// writeString escapes as jq 1.7 does: quote, backslash and control characters
// (DEL as \u007f); every other character, non-ASCII included, is written raw.
func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
