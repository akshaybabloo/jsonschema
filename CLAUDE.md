# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`github.com/akshaybabloo/jsonschema` is a Go library that generates JSON Schema (draft 2020-12) documents from Go types using reflection. It is a fork of `alecthomas/jsonschema` with breaking differences: auto-generated schema `$id`s based on package path, draft 2020-12 instead of draft-04, no `FullyQualifyTypeName` option, and no `yaml` tag support.

Go 1.26+ is required (per `go.mod`).

## Commands

- Run all tests with race detector and coverage: `go test -race -coverprofile=coverage.out -covermode=atomic ./...` (this is what CI runs).
- Run a single test: `go test -run TestSchemaGeneration/test_user ./...` (subtest names come from the fixture filename without the `.json` extension — see `reflect_test.go:478`).
- **Regenerate fixtures** after an intentional output change: `go test -update ./...`. This rewrites every `fixtures/*.json` from live reflector output — always review the diff before committing.
- **Diagnose a failing fixture**: `go test -compare ./...` writes the actual output next to the expected file as `fixtures/<name>.out.json` so you can diff them.
- Lint: `golangci-lint run` (CI pins `v2.11.4`). Config in `.golangci.yml` includes a `gofmt` rewrite rule that normalizes `interface{}` → `any` and `a[b:len(a)]` → `a[b:]` — do not reintroduce the long forms.

Releases are cut by pushing to the `release` branch; `anothrNick/github-tag-action` auto-tags the merge (`.github/workflows/release.yaml`). Do not create tags by hand.

## Architecture

Five source files make up the library; each has a distinct role:

- **`schema.go`** — defines the `Schema` struct, which is a direct 1:1 mirror of the JSON Schema draft 2020-12 specification (field comments cite RFC sections). `Properties` uses `wk8/go-ordered-map/v2` so field order in generated schemas matches struct declaration order. `Extras map[string]any` is serialized into the parent object (not nested) via custom marshalling — this is how `jsonschema_extras` tags surface.
- **`reflect.go`** — the engine. `Reflector` holds every configuration knob (`ExpandedStruct`, `DoNotReference`, `AllowAdditionalProperties`, `Anonymous`, `AssignAnchor`, `RequiredFromJSONSchemaTags`, `Namer`, `KeyNamer`, `Mapper`, `Lookup`, `IgnoredTypes`, `AdditionalFields`, `LookupComment`, `CommentMap`, `FieldNameTag`, `BaseSchemaID`). Entry points are `Reflect(v any)` / `ReflectFromType(t reflect.Type)` (package-level helpers use a zero-value `Reflector`). Reflection recurses via `reflectTypeToSchema` → per-kind handlers (`reflectStruct`, `reflectSliceOrArray`, `reflectMap`). The `definitions` map is threaded through every call so recursive/self-referential types emit `$ref`s instead of looping.
- **`reflect_comments.go`** — `AddGoComments(base, path)` walks Go source with `go/parser` and populates `Reflector.CommentMap`, keyed by `"<pkgImportPath>.<TypeName>"` and `"<pkgImportPath>.<TypeName>.<FieldName>"`. The `base` argument is required because `go/parser` cannot determine a package's fully qualified import path on its own.
- **`id.go`** — `ID` is a string type representing a schema URI. `Validate/Base/Add/Def/Anchor` manipulate the URI. Auto-generated IDs use `"https://" + t.PkgPath() + "/" + ToSnakeCase(typeName)` when `BaseSchemaID` is unset and `Anonymous` is false.
- **`utils.go`** — `ToSnakeCase` (dash-separated, for IDs) and `NewProperties` (type-aliased ordered-map constructor).

### Custom schema hooks

Types can implement any of four optional methods to customize reflection output. **All four MUST be defined on a non-pointer receiver** or reflection will not find them (`t.Implements(...)` on the non-pointer type):

- `JSONSchema() *Schema` — short-circuits auto-generation; use when MarshalJSON serializes the type to something unrelated to its Go layout (e.g. a string).
- `JSONSchemaExtend(*Schema)` — runs *after* auto-generation so you can tweak the result.
- `JSONSchemaAlias() any` — return a different value; its type is reflected instead of the original.
- `JSONSchemaProperty(prop string) any` — called per property; return an alternative value whose type is reflected in place of that field.

### Struct tags the reflector reads

- `json:"name,omitempty"` — standard; drives property name and (inversely) required-ness.
- `jsonschema:"..."` — comma-separated directives: `required`, `minimum=`, `maximum=`, `exclusiveMinimum=`, `exclusiveMaximum=`, `minLength=`, `maxLength=`, `pattern=`, `format=`, `enum=` (repeatable), `default=`, `example=` (repeatable), `title=`, `description=`, `readOnly=`, `writeOnly=`, `nullable`, `anchor=`, `oneof_required=<group>`, `oneof_type=a;b`, `oneof_ref=A;B`, `anyof_required=`, `anyof_type=`. See `reflect_test.go` `TestUser` for a comprehensive reference.
- `jsonschema_description:"..."` — dedicated description tag (avoids needing to escape commas).
- `jsonschema_extras:"k=v,k=v"` — arbitrary extra keys merged into the property schema via `Schema.Extras`.

### Testing pattern — fixtures

Every high-level reflector behavior is validated by reflecting a test type and comparing the marshalled output against a file in `fixtures/`. The table of `{type, Reflector config, fixture path}` lives in `TestSchemaGeneration` in `reflect_test.go`. When adding a new feature:

1. Define a test type in `reflect_test.go`.
2. Add an entry to the table with a new `fixtures/<name>.json` path.
3. Run `go test -update ./...` to generate the fixture.
4. Review the generated fixture carefully — the test itself is only as strong as the committed JSON.
