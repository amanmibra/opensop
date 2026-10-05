package yamlpy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/pyyaml_corpus.json holds PyYAML 6 safe_load results (Python repr) for tricky
// inputs. Regenerate it with PyYAML if the corpus changes.
func TestMatchesPyYAML(t *testing.T) {
	data, err := os.ReadFile("testdata/pyyaml_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		In            string `json:"in"`
		Repr          string `json:"repr"`
		Error         bool   `json:"error"`
		MappingValues bool   `json:"mapping_values"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, c := range corpus {
		v, err := SafeLoad(c.In)
		if c.Error {
			if err == nil {
				t.Errorf("%q: PyYAML fails, got %s", c.In, v.Repr())
			} else if got := strings.Contains(err.Error(), "mapping values are not allowed"); got != c.MappingValues {
				t.Errorf("%q: mapping-values hint %v, PyYAML %v (%v)", c.In, got, c.MappingValues, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.In, err)
			continue
		}
		got := "None"
		if v != nil {
			got = v.Repr()
		}
		if got != c.Repr {
			t.Errorf("%q:\n  got  %s\n  want %s", c.In, got, c.Repr)
		}
	}
}
