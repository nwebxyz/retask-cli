// internal/output/fields.go
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
)

// Presets maps a preset name to the fields it selects. On the command line a
// preset is written with an "@" prefix (--fields @short), which keeps preset
// names apart from field names: no JSON field name starts with "@".
type Presets map[string][]string

// FieldSet backs a list command's --fields flag: the top-level JSON fields its
// items expose and the presets it offers.
type FieldSet struct {
	fields  []string
	presets Presets
}

// MustFieldSet returns the FieldSet for list items of type T (a struct or a
// pointer to one). It panics if a preset names a field T does not have, so a
// bad preset fails every test run rather than only the user who selects it.
func MustFieldSet[T any](presets Presets) *FieldSet {
	s := &FieldSet{fields: jsonFieldNames(reflect.TypeFor[T]()), presets: presets}
	for name, fields := range presets {
		for _, f := range fields {
			if !slices.Contains(s.fields, f) {
				panic(fmt.Sprintf("output: preset @%s names unknown field %q", name, f))
			}
		}
	}
	return s
}

// Usage returns the help text for a --fields flag backed by s.
func (s *FieldSet) Usage() string {
	var presets []string
	for _, name := range slices.Sorted(maps.Keys(s.presets)) {
		presets = append(presets, fmt.Sprintf("@%s (%s)", name, strings.Join(s.presets[name], ", ")))
	}
	return "Comma-separated fields to output, in order. Presets: " + strings.Join(presets, "; ")
}

// Print writes v to os.Stdout like Print, keeping only the fields spec selects.
func (s *FieldSet) Print(pretty bool, v any, spec string) error {
	return s.Fprint(os.Stdout, pretty, v, spec)
}

// Fprint writes the list v like Fprint, keeping only the fields spec (the
// --fields value) selects, in spec order. Fields a row does not carry are
// omitted from its JSON object and left blank in the table. An empty spec
// writes every field.
func (s *FieldSet) Fprint(w io.Writer, pretty bool, v any, spec string) error {
	if strings.TrimSpace(spec) == "" {
		return Fprint(w, pretty, v)
	}
	fields, err := s.resolve(spec)
	if err != nil {
		return err
	}

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return Fprint(w, pretty, v)
	}

	if pretty {
		cells := make([][]string, len(rows))
		for i, row := range rows {
			cells[i] = make([]string, len(fields))
			for j, f := range fields {
				raw, ok := row[f]
				if !ok {
					continue
				}
				var val any
				if err := json.Unmarshal(raw, &val); err != nil {
					return err
				}
				cells[i][j] = fmt.Sprintf("%v", val)
			}
		}
		return writeTable(w, fields, cells)
	}

	objects := make([]orderedObject, len(rows))
	for i, row := range rows {
		for _, f := range fields {
			if raw, ok := row[f]; ok {
				objects[i] = append(objects[i], member{key: f, value: raw})
			}
		}
	}
	return Fprint(w, false, objects)
}

// resolve expands spec into the de-duplicated fields it selects, keeping the
// position of each field's first appearance.
func (s *FieldSet) resolve(spec string) (fields []string, err error) {
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		var selected []string
		switch {
		case name == "":
			continue
		case strings.HasPrefix(name, "@"):
			preset, ok := s.presets[strings.TrimPrefix(name, "@")]
			if !ok {
				var valid []string
				for _, p := range slices.Sorted(maps.Keys(s.presets)) {
					valid = append(valid, "@"+p)
				}
				return nil, fmt.Errorf("unknown preset %q in --fields. Valid presets: %s", name, strings.Join(valid, ", "))
			}
			selected = preset
		case slices.Contains(s.fields, name):
			selected = []string{name}
		default:
			return nil, fmt.Errorf("unknown field %q in --fields. Valid fields: %s", name, strings.Join(s.fields, ", "))
		}
		for _, f := range selected {
			if !slices.Contains(fields, f) {
				fields = append(fields, f)
			}
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("--fields %q selects no fields", spec)
	}
	return fields, nil
}

// jsonFieldNames returns the top-level keys encoding/json emits for t, in
// declaration order: exported fields named by their json tag, or by their Go
// name when untagged (as proto oneof fields are).
func jsonFieldNames(t reflect.Type) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("output: field selection needs a struct type, got %s", t))
	}
	var names []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// orderedObject is a JSON object that keeps its members in slice order,
// unlike a map, which encoding/json writes with sorted keys.
type orderedObject []member

type member struct {
	key   string
	value json.RawMessage
}

func (o orderedObject) MarshalJSON() (data []byte, err error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
