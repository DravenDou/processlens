package graph

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseWorkflowFixture(t *testing.T) {
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	w, err := Parse(b)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(w.Steps) != 6 || len(w.Checkpoints) != 6 || len(w.Objects) != 6 {
		t.Fatalf("want 6/6/6 steps/checkpoints/objects, got %d/%d/%d",
			len(w.Steps), len(w.Checkpoints), len(w.Objects))
	}
}

func TestParseToleratesFences(t *testing.T) {
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	w, err := Parse([]byte("```json\n" + string(b) + "\n```"))
	if err != nil {
		t.Fatalf("parse fenced: %v", err)
	}
	if w.Name != "block-tower" {
		t.Fatalf("want name block-tower, got %q", w.Name)
	}
}

func TestValidateRejects(t *testing.T) {
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	mustParse := func(t *testing.T, raw string) *Workflow {
		t.Helper()
		w, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("setup parse failed: %v", err)
		}
		return w
	}
	base := string(b)

	t.Run("non-contiguous index", func(t *testing.T) {
		w := mustParse(t, base)
		w.Steps[2].Index = 99
		if err := w.Validate(); err == nil {
			t.Fatal("want error for bad index, got nil")
		}
	})
	t.Run("unknown object ref", func(t *testing.T) {
		w := mustParse(t, base)
		w.Steps[0].Objects = []string{"nope"}
		if err := w.Validate(); err == nil {
			t.Fatal("want error for unknown object, got nil")
		}
	})
	t.Run("grid out of bounds", func(t *testing.T) {
		w := mustParse(t, base)
		w.Checkpoints[0].Snapshot[0].Col = 7
		if err := w.Validate(); err == nil {
			t.Fatal("want error for out-of-bounds grid, got nil")
		}
	})
	t.Run("checkpoint count", func(t *testing.T) {
		w := mustParse(t, base)
		w.Checkpoints = w.Checkpoints[:5]
		if err := w.Validate(); err == nil {
			t.Fatal("want error for checkpoint count, got nil")
		}
	})
	t.Run("unknown field rejected", func(t *testing.T) {
		bad := strings.Replace(base, `"name": "block-tower"`, `"name": "block-tower", "zzz": 1`, 1)
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("want error for unknown field, got nil")
		}
	})
}

func TestPromptsRoundTrip(t *testing.T) {
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	w, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	w.Prompts = []string{"screwdriver", "hammer"}
	enc, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Prompts, w.Prompts) {
		t.Fatalf("prompts = %q, want %q", back.Prompts, w.Prompts)
	}
}

func TestParseLegacyWithoutPrompts(t *testing.T) {
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "prompts") {
		t.Fatal("fixture gained a prompts key; this test needs a legacy file")
	}
	w, err := Parse(b)
	if err != nil {
		t.Fatalf("legacy file must still parse+validate: %v", err)
	}
	if len(w.Prompts) != 0 {
		t.Fatalf("legacy prompts = %q, want empty", w.Prompts)
	}
}
