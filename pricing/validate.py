"""Validate editable provider files against schema.json (requires jsonschema)."""
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker

root = Path(__file__).resolve().parent
schema = json.loads((root / "schema.json").read_text())
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema, format_checker=FormatChecker())
for path in sorted((root / "providers").glob("*.json")):
    validator.validate(json.loads(path.read_text()))
    print(f"Validated {path.name}")
