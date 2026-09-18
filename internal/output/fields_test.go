// internal/output/fields_test.go
package output_test

import (
	"bytes"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// item mirrors the shape of a generated proto message: omitempty json tags,
// an untagged exported field (proto oneofs), and fields encoding/json skips.
type item struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Status   string `json:"status,omitempty"`
	Oneof    string
	Internal string `json:"-"`
	secret   string
}

var itemFields = output.MustFieldSet[*item](output.Presets{"short": {"id", "name"}})

var items = []*item{
	{ID: "a1", Name: "Alpha", Status: "READY", Oneof: "x"},
	{ID: "b2", Name: "Beta"},
}

func TestFieldSetFprintJSON(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "fields in requested order",
			spec: "name,id",
			want: `[
  {
    "name": "Alpha",
    "id": "a1"
  },
  {
    "name": "Beta",
    "id": "b2"
  }
]
`,
		},
		{
			name: "field absent from a row is omitted",
			spec: "id,status",
			want: `[
  {
    "id": "a1",
    "status": "READY"
  },
  {
    "id": "b2"
  }
]
`,
		},
		{
			name: "preset expands to its fields",
			spec: "@short",
			want: `[
  {
    "id": "a1",
    "name": "Alpha"
  },
  {
    "id": "b2",
    "name": "Beta"
  }
]
`,
		},
		{
			name: "preset combines with fields and duplicates keep first position",
			spec: "status, @short ,name",
			want: `[
  {
    "status": "READY",
    "id": "a1",
    "name": "Alpha"
  },
  {
    "id": "b2",
    "name": "Beta"
  }
]
`,
		},
		{
			name: "untagged exported field uses its Go name",
			spec: "Oneof",
			want: `[
  {
    "Oneof": "x"
  },
  {
    "Oneof": ""
  }
]
`,
		},
		{
			name: "empty spec prints every field",
			spec: "",
			want: `[
  {
    "id": "a1",
    "name": "Alpha",
    "status": "READY",
    "Oneof": "x"
  },
  {
    "id": "b2",
    "name": "Beta",
    "Oneof": ""
  }
]
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, itemFields.Fprint(&buf, false, items, tt.spec))
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestFieldSetFprintTable(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, itemFields.Fprint(&buf, true, items, "name,status"))
	assert.Equal(t, "name   status\nAlpha  READY\nBeta   \n", buf.String())
}

func TestFieldSetFprintEmptyList(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, itemFields.Fprint(&buf, false, []*item{}, "name"))
	assert.Equal(t, "[]\n", buf.String())
}

func TestFieldSetFprintRejectsUnknownNames(t *testing.T) {
	tests := []struct {
		name    string
		v       []*item
		spec    string
		wantErr []string
	}{
		{name: "unknown field", v: items, spec: "id,nope", wantErr: []string{`"nope"`, "id, name, status, Oneof"}},
		{name: "unknown field on empty list", v: []*item{}, spec: "nope", wantErr: []string{`"nope"`}},
		{name: "field skipped by encoding/json", v: items, spec: "Internal", wantErr: []string{`"Internal"`}},
		{name: "unexported field", v: items, spec: "secret", wantErr: []string{`"secret"`}},
		{name: "unknown preset", v: items, spec: "@nope", wantErr: []string{`"@nope"`, "@short"}},
		{name: "only separators", v: items, spec: " , ", wantErr: []string{"--fields"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := itemFields.Fprint(&buf, false, tt.v, tt.spec)
			require.Error(t, err)
			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, buf.String())
		})
	}
}

func TestFieldSetUsageListsPresets(t *testing.T) {
	assert.Contains(t, itemFields.Usage(), "@short (id, name)")
}

func TestMustFieldSetPanicsOnUnknownPresetField(t *testing.T) {
	assert.Panics(t, func() {
		output.MustFieldSet[*item](output.Presets{"short": {"id", "missing"}})
	})
}
