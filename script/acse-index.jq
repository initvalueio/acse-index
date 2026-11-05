# Copyright 2026 The acse-index Authors
# SPDX-License-Identifier: Apache-2.0
#
# acse-index, jq form. Reads `terraform show -json` (state, after apply) and
# writes a resource-index-1.0 document: per managed resource only its address,
# type, provider, module path and, for the cloud providers ACSE scans, its
# cloud id. It never writes an attribute value. Output is byte-identical to the
# Go binary's for the same input and arguments.
#
# Run it through script/acse-index.sh, or directly (jq 1.6 or later):
#   terraform show -json | jq -s -f acse-index.jq \
#     --arg repo "$REPO" --arg dir "$DIR" --arg commit "$SHA" \
#     --arg run "$RUN_REF" --arg produced_at ""

def acse_index_version: "0.1.0";
def max_resources: 50000;

def fail($msg): error("acse-index: " + $msg);

def has_control: explode | any(.[]; . < 32 or . == 127);

# 1..$max code points, no control character.
def printable($max): type == "string" and length >= 1 and length <= $max and (has_control | not);

# The fixed attribute order per scanned cloud provider.
# Every other provider keeps no id: random_string's id is its result.
def cloud_attrs($family):
  if $family == "aws" then ["arn", "id"]
  elif $family == "google" or $family == "google-beta" then ["self_link", "id"]
  elif $family == "azurerm" or $family == "azuread" then ["id"]
  else [] end;

def cloud_family:
  (capture("/hashicorp/(?<f>aws|google|google-beta|azurerm|azuread)\\z") // null) | if . == null then null else .f end;

def cloud_id($provider):
  . as $r
  | ($provider | cloud_family) as $family
  | if $family == null then null
    else
      (if ($r.values | type) == "object" then $r.values else null end) as $values
      | (if ($r.sensitive_values | type) == "object" then $r.sensitive_values else null end) as $sensitive
      | first(
          (cloud_attrs($family)[]
           | . as $attr
           | select(($sensitive != null and $sensitive[$attr] == true) | not)
           | select($values != null)
           | $values[$attr]
           | select(printable(2048))),
          null)
    end;

def opt_object($key):
  if has($key) | not then null
  elif .[$key] == null then null
  elif (.[$key] | type) == "object" then .[$key]
  else fail("\($key) is not an object") end;

def opt_array($key):
  if has($key) | not then []
  elif .[$key] == null then []
  elif (.[$key] | type) == "array" then .[$key]
  else fail("\($key) is not an array") end;

def resource($module_path):
  if type != "object" then fail("a resource is not an object") else . end
  | if (.mode | type) != "string" then fail("a resource has no mode") else . end
  | if .mode == "data" then empty
    elif .mode != "managed" then fail("a resource has an unknown mode")
    else
      if (.address | printable(1024)) | not then fail("a managed resource has no valid address") else . end
      | if .address | test("\\A(module\\.[^.]+\\.)*data\\.") then empty
        else
          .address as $address
          | if (.type | type) == "string" and (.type | test("\\A[a-z][a-z0-9_]{0,127}\\z")) then . else fail("\($address): resource type is not valid") end
          | if (.provider_name | type) == "string" and (.provider_name | test("\\A[A-Za-z0-9.-]{1,253}(/[A-Za-z0-9_.-]{1,64}){1,2}\\z")) then . else fail("\($address): provider_name is not a provider source address") end
          | cloud_id(.provider_name) as $cid
          | {address: $address, type: .type, provider: .provider_name}
            + (if $module_path == null then {} else {module_path: $module_path} end)
            + (if $cid == null then {} else {cloud_id: $cid} end)
        end
    end;

def walk_module($is_root):
  (if $is_root then null
   else
     .address
     | if (type == "string") and startswith("module.") and printable(1022) and length >= 8 then .
       else fail("a child module has no valid module address") end
   end) as $module_path
  | (opt_array("resources")[] | resource($module_path)),
    (opt_array("child_modules")[]
     | if type != "object" then fail("a child module is not an object") else . end
     | walk_module(false));

def check_header:
  if ($repo | test("\\A[A-Za-z0-9][A-Za-z0-9._-]{0,99}(/[A-Za-z0-9][A-Za-z0-9._-]{0,99}){1,3}\\z")) | not then
    fail("--repo must be owner/name (letters, digits, . _ -; up to four segments)") else . end
  | if $dir != "." and ((($dir | printable(512)) | not) or ($dir | startswith("/")) or ($dir | startswith("."))) then
    fail("--dir must be . or a repository-relative path not starting with / or .") else . end
  | if ($commit | test("\\A([0-9a-f]{40}|[0-9a-f]{64})\\z")) | not then
    fail("--commit must be a full lowercase hex commit id (40 or 64 characters)") else . end
  | if ($run | test("\\A[A-Za-z0-9][A-Za-z0-9._:/@-]{0,255}\\z")) | not then
    fail("--run must start with a letter or digit and use only letters, digits and . _ : / @ -") else . end;

def produced_at:
  if $produced_at == "" then now | todate
  elif ($produced_at | test("\\A[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\\z")) | not then
    fail("--produced-at must be YYYY-MM-DDTHH:MM:SSZ")
  elif ((try ($produced_at | fromdateiso8601 | todate) catch null) != $produced_at) then
    fail("--produced-at is not a valid UTC time")
  else $produced_at end;

check_header
| produced_at as $at
| if length == 0 then fail("no input: pipe `terraform show -json` into acse-index")
  elif length > 1 then fail("input holds more than one JSON document")
  else .[0] end
| if type != "object" then fail("input is not a `terraform show -json` document") else . end
| if has("planned_values") or has("resource_changes") then
    fail("input is a plan; run `terraform show -json` on the state after apply") else . end
| (opt_object("values")) as $values
| (if $values == null then []
   else ($values | opt_object("root_module")) as $root
     | if $root == null then [] else [$root | walk_module(true)] end
   end) as $resources
| if ($resources | length) > max_resources then fail("more than \(max_resources) managed resources in one root") else . end
| {
    resource_index_format_version: "1.0",
    repository: $repo,
    working_directory: $dir,
    commit: $commit,
    run_ref: $run,
    produced_at: $at,
    producer: {name: "acse-index", version: acse_index_version},
    resources: $resources
  }
