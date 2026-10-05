package difflib

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// testdata/python.json is generated with Python's difflib from random inputs.
func TestMatchesPython(t *testing.T) {
	data, err := os.ReadFile("testdata/python.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Matcher []struct {
			A, B    []string
			Ratio   float64
			Opcodes [][]any
		}
		Diffs []struct {
			A, B []string
			Diff string
		}
	}
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatal(err)
	}
	for i, c := range ref.Matcher {
		sm := New(c.A, c.B, false)
		if math.Abs(sm.Ratio()-c.Ratio) > 0 {
			t.Errorf("case %d: ratio %v, Python %v", i, sm.Ratio(), c.Ratio)
		}
		got := sm.OpCodes()
		if len(got) != len(c.Opcodes) {
			t.Errorf("case %d: %d opcodes, Python %d", i, len(got), len(c.Opcodes))
			continue
		}
		for j, op := range c.Opcodes {
			want := OpCode{op[0].(string), int(op[1].(float64)), int(op[2].(float64)), int(op[3].(float64)), int(op[4].(float64))}
			if got[j] != want {
				t.Errorf("case %d opcode %d: %v, Python %v", i, j, got[j], want)
			}
		}
	}
	for i, d := range ref.Diffs {
		from, to := "a/x.prompt.md", "b/x.prompt.md"
		if strings.HasPrefix(d.Diff, "--- a/x\n") {
			from, to = "a/x", "b/x"
		}
		if got := UnifiedDiff(d.A, d.B, from, to); got != d.Diff {
			t.Errorf("diff %d differs:\n%s\n---- Python:\n%s", i, got, d.Diff)
		}
	}
}
