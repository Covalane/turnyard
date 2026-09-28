package contracts

import (
	"embed"
	"encoding/json"
	"sync"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

var compiledSchemas = struct {
	sync.Once
	values map[string]*jsonschema.Schema
	err    error
}{}

func schemaFor(kind string) (*jsonschema.Schema, error) {
	compiledSchemas.Do(func() {
		compiledSchemas.values = map[string]*jsonschema.Schema{}
		for _, name := range []string{"session", "environment", "work"} {
			body, err := schemaFiles.ReadFile("schemas/" + name + ".json")
			if err != nil {
				compiledSchemas.err = err
				return
			}
			var document any
			if err := json.Unmarshal(body, &document); err != nil {
				compiledSchemas.err = err
				return
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(name+".json", document); err != nil {
				compiledSchemas.err = err
				return
			}
			compiledSchemas.values[name], compiledSchemas.err = compiler.Compile(name + ".json")
			if compiledSchemas.err != nil {
				return
			}
		}
	})
	if compiledSchemas.err != nil {
		return nil, compiledSchemas.err
	}
	schema := compiledSchemas.values[kind]
	if schema == nil {
		return nil, fault.New(fault.CodeInvalidSpec, "unknown schema kind %s", kind)
	}
	return schema, nil
}
