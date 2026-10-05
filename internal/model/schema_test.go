package model

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/amanmibra/opensop/internal/pyx"
)

// spec/*.schema.json are the published schemas (generated from the Python models). The Go
// structs must accept exactly the same fields, in the same order (the order also fixes the
// JSON that lock.json hashes), with the same required fields and choices.

type schema struct {
	Properties orderedKeys                `json:"properties"`
	Required   []string                   `json:"required"`
	Defs       map[string]json.RawMessage `json:"$defs"`
}

// orderedKeys reads a JSON object's keys in order.
type orderedKeys []string

func (o *orderedKeys) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		*o = append(*o, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}

func readSchema(t *testing.T, name string) (schema, map[string]map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "spec", name))
	if err != nil {
		t.Fatal(err)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	json.Unmarshal(data, &raw)
	return s, raw.Properties
}

func without(list []string, drop ...string) []string {
	out := []string{}
	for _, x := range list {
		keep := true
		for _, d := range drop {
			keep = keep && x != d
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}

func TestStructsMatchPublishedSchemas(t *testing.T) {
	for _, c := range []struct {
		kind, file string
		drop       []string // fields set from the file rather than written in it
	}{
		{"base", "base.schema.json", []string{"text"}},
		{"sop", "sop.schema.json", nil},
		{"agent", "agent.schema.json", nil},
		{"opensop", "opensop.schema.json", nil},
	} {
		s, _ := readSchema(t, c.file)
		names, required := FieldNames(c.kind)
		if got := without(names, c.drop...); !reflect.DeepEqual(got, []string(s.Properties)) {
			t.Errorf("%s: fields %v, schema %v", c.file, got, s.Properties)
		}
		if got := without(required, append(c.drop, "id")...); !reflect.DeepEqual(got, append([]string{}, s.Required...)) {
			t.Errorf("%s: required %v, schema %v", c.file, got, s.Required)
		}
	}

	_, base := readSchema(t, "base.schema.json")
	if !reflect.DeepEqual(base["position"]["enum"], []any{"top", "bottom"}) || !reflect.DeepEqual(Positions, []string{"top", "bottom"}) {
		t.Error("position choices differ from the schema")
	}
	sop, sopProps := readSchema(t, "sop.schema.json")
	enum := sopProps["delivery"]["enum"].([]any)
	if len(enum) != len(Deliveries) {
		t.Error("delivery choices differ from the schema")
	}
	for i, d := range Deliveries {
		if enum[i] != d {
			t.Error("delivery choices differ from the schema")
		}
	}
	var step schema
	if err := json.Unmarshal(sop.Defs["Step"], &step); err != nil {
		t.Fatal(err)
	}
	names, required := FieldNames("step")
	if !reflect.DeepEqual(names, []string(step.Properties)) || !reflect.DeepEqual(required, step.Required) {
		t.Errorf("Step: %v %v, schema %v %v", names, required, step.Properties, step.Required)
	}
	_, cfg := readSchema(t, "opensop.schema.json")
	if cfg["version"]["const"] != float64(1) {
		t.Error("version const differs")
	}
}

// The canonical JSON (hashed into lock.json) must list fields in declaration order.
func TestDumpJSONKeysFollowFieldOrder(t *testing.T) {
	keys := func(js string) []string {
		var k orderedKeys
		if err := json.Unmarshal([]byte(js), &k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	lk := "x"
	for kind, js := range map[string]string{
		"base":  (&Base{}).DumpJSON(),
		"sop":   (&SOP{}).DumpJSON(),
		"agent": (&Agent{Livekit: &lk}).DumpJSON(),
	} {
		names, _ := FieldNames(kind)
		if got := keys(js); !reflect.DeepEqual(got, names) {
			t.Errorf("%s JSON keys %v, fields %v", kind, got, names)
		}
	}
	step := pyx.DumpsCompact(stepsJSON([]Step{{Text: "a"}})[0])
	names, _ := FieldNames("step")
	if got := keys(step); !reflect.DeepEqual(got, names) {
		t.Errorf("step JSON keys %v, fields %v", got, names)
	}
}
