package policy

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema is the runtime structural contract. The published schema copy is
// checked byte-for-byte by tests so IDE and runtime validation cannot drift.
//
//go:embed schema.json
var Schema []byte

var structural = func() *jsonschema.Schema {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema))
	if err != nil {
		panic(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("policy.json", document); err != nil {
		panic(err)
	}
	result, err := compiler.Compile("policy.json")
	if err != nil {
		panic(err)
	}
	return result
}()

func validateStructure(data []byte) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := structural.Validate(document); err != nil {
		return fmt.Errorf("policy schema: %w", err)
	}
	return nil
}
