# Copyright 2026 The acse-index Authors
# SPDX-License-Identifier: Apache-2.0
"""Every golden index validates against the mirrored resource-index-1.0
schema, and the upstream contract's own fixtures behave as upstream says:
the happy path is accepted; a kept value, a data source and a utility
provider's id are refused."""
import json
import pathlib
import sys

from jsonschema import Draft202012Validator, FormatChecker

root = pathlib.Path(__file__).resolve().parent.parent
schema = json.loads((root / "schema/resource-index-1.0.schema.json").read_text())
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema, format_checker=FormatChecker())

failed = False
goldens = sorted((root / "testdata/golden").glob("*.json"))
if not goldens:
    sys.exit("no golden files")
for path in goldens + [root / "testdata/schema/valid-core-happy-path.json"]:
    errors = list(validator.iter_errors(json.loads(path.read_text())))
    print(("ok  " if not errors else "FAIL") + f" valid   {path.name}")
    for e in errors:
        print(f"     {list(e.absolute_path)}: {e.message}")
    failed |= bool(errors)
for path in sorted((root / "testdata/schema").glob("invalid-*.json")):
    refused = not validator.is_valid(json.loads(path.read_text()))
    print(("ok  " if refused else "FAIL") + f" refused {path.name}")
    failed |= not refused
sys.exit(1 if failed else 0)
