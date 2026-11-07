// Package contract owns wire DTOs and strict source-schema validation. It has no
// storage, model, HTTP handler or publication service implementation.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"yuanfa-official/schemas"
)

const SchemaVersion = 1
const schemaBase = "https://yuanfa.example/schemas/"

// BusinessError is safe to expose over HTTP or MCP. Never store internal causes
// or private paths in Message. HTTPStatus is transport metadata, not wire data.
type BusinessError struct {
	HTTPStatus int
	Detail     ErrorDetail
}

func (e *BusinessError) Error() string           { return e.Detail.Code + ": " + e.Detail.Message }
func (e *BusinessError) Response() ErrorResponse { return ErrorResponse{Error: e.Detail} }
func NewBusinessError(status int, code, message string, fields ...string) *BusinessError {
	if fields == nil {
		fields = []string{}
	}
	return &BusinessError{HTTPStatus: status, Detail: ErrorDetail{Code: code, Message: message, Fields: fields}}
}

var initialize sync.Once
var sourceSchemas = make(map[string]map[string]any)
var compiled map[string]*jsonschema.Schema
var initializationError error

func loadSchemas() {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	entries, err := schemas.Files.ReadDir(".")
	if err != nil {
		initializationError = err
		return
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		data, err := schemas.Files.ReadFile(entry.Name())
		if err != nil {
			initializationError = err
			return
		}
		var document map[string]any
		if err = json.Unmarshal(data, &document); err != nil {
			initializationError = err
			return
		}
		if err = compiler.AddResource(schemaBase+entry.Name(), document); err != nil {
			initializationError = err
			return
		}
		sourceSchemas[entry.Name()] = document
		defs := document["$defs"].(map[string]any)
		for name := range defs {
			names = append(names, entry.Name()+"#/$defs/"+name)
		}
	}
	compiled = make(map[string]*jsonschema.Schema, len(names))
	for _, name := range names {
		sch, err := compiler.Compile(schemaBase + name)
		if err != nil {
			initializationError = err
			return
		}
		compiled[name] = sch
	}
}

// ValidateJSON checks required/null/unknown fields before any typed decoding.
// schema is "guide.schema.json#/$defs/GuideRequirements", for example.
func ValidateJSON(schema string, data []byte) error {
	_, err := validatedValue(schema, data)
	return err
}

func validatedValue(schema string, data []byte) (any, error) {
	initialize.Do(loadSchemas)
	if initializationError != nil {
		return nil, fmt.Errorf("compile shared contracts: %w", initializationError)
	}
	sch, ok := compiled[schema]
	if !ok {
		return nil, fmt.Errorf("unknown contract schema %q", schema)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, NewBusinessError(400, "INVALID_JSON", "JSON 格式不合法。")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, NewBusinessError(400, "INVALID_JSON", "仅接受一个 JSON 值。")
	}
	document := strings.Split(schema, "#")[0]
	value = normalizeValue(map[string]any{"$ref": schema}, value, document)
	if err := sch.Validate(value); err != nil {
		var validation *jsonschema.ValidationError
		if !errors.As(err, &validation) {
			return nil, err
		}
		fields := make([]string, 0)
		collectFields(validation, &fields)
		slices.Sort(fields)
		fields = slices.Compact(fields)
		return nil, NewBusinessError(422, "VALIDATION_FAILED", "字段或引用不合法。", fields...)
	}
	if err := validateInvariants(schema, value); err != nil {
		return nil, err
	}
	return value, nil
}

func collectFields(e *jsonschema.ValidationError, fields *[]string) {
	if len(e.Causes) > 0 {
		for _, cause := range e.Causes {
			collectFields(cause, fields)
		}
		return
	}
	path := strings.Join(e.InstanceLocation, ".")
	if required, ok := e.ErrorKind.(*kind.Required); ok {
		for _, name := range required.Missing {
			if path != "" {
				*fields = append(*fields, path+"."+name)
			} else {
				*fields = append(*fields, name)
			}
		}
		return
	}
	if additional, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
		for _, name := range additional.Properties {
			if path != "" {
				*fields = append(*fields, path+"."+name)
			} else {
				*fields = append(*fields, name)
			}
		}
		return
	}
	if path == "" {
		path = "$"
	}
	*fields = append(*fields, path)
}

// Decode validates the source schema first, so Go zero values cannot hide a
// missing key or turn an explicit null into false/0/an empty string.
func Decode[T any](schema string, data []byte) (T, error) {
	var result T
	value, err := validatedValue(schema, data)
	if err != nil {
		return result, err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(normalized, &result); err != nil {
		var typed *json.UnmarshalTypeError
		if errors.As(err, &typed) {
			field := typed.Field
			if field == "" {
				field = "$"
			}
			return result, NewBusinessError(422, "VALIDATION_FAILED", "字段数值或类型无法表示。", field)
		}
		return result, err
	}
	return result, nil
}

func UnknownRequirements() GuideRequirements {
	return GuideRequirements{SceneIDs: []SceneID{}}
}
