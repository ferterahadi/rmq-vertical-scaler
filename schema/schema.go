// Package schema embeds the config JSON Schema and validates config files
// against it. The schema file is the single source of truth for what a valid
// generate config looks like — the CLI, IDEs (via $schema), and the docs all
// reference the same document.
package schema

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed config-schema.json
var ConfigSchema []byte

// compiled is built at package load; a malformed embedded schema is a
// programmer error and panics here rather than per-call (same convention as
// the embedded deployment template in internal/manifests).
var compiled = mustCompile()

func mustCompile() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(ConfigSchema))
	if err != nil {
		panic(fmt.Sprintf("embedded config schema is not valid JSON: %v", err))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("config-schema.json", doc); err != nil {
		panic(fmt.Sprintf("add embedded config schema: %v", err))
	}
	s, err := c.Compile("config-schema.json")
	if err != nil {
		panic(fmt.Sprintf("compile embedded config schema: %v", err))
	}
	return s
}

// Validate checks a raw config JSON document against the embedded schema.
// The returned error lists every violation with its JSON path.
func Validate(configJSON []byte) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(configJSON))
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if err := compiled.Validate(inst); err != nil {
		return fmt.Errorf("config does not match schema: %w", err)
	}
	return nil
}
