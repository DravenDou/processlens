package verify

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"processlens/internal/graph"
)

func loadWorkflow(t *testing.T) *graph.Workflow {
	t.Helper()
	b, err := os.ReadFile("../../testdata/workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	w, err := graph.Parse(b)
	if err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return w
}

func findingPairs(r Report) map[[2]string]bool {
	m := map[[2]string]bool{}
	for _, f := range r.Findings {
		m[[2]string{f.Type, f.StepID}] = true
	}
	return m
}

func TestCompareCleanExecutionPasses(t *testing.T) {
	w := loadWorkflow(t)
	var observed []ObservedStep
	for i, s := range w.Steps {
		cp := w.CheckpointFor(s.ID)
		observed = append(observed, ObservedStep{
			StepID:   s.ID,
			TSeconds: float64((i + 1) * 8),
			Snapshot: append([]graph.SnapshotItem(nil), cp.Snapshot...),
		})
	}
	rep := Compare(w, "video1-clean.mp4", observed)
	if !rep.Passed || len(rep.Findings) != 0 {
		t.Fatalf("want pass with 0 findings, got %+v", rep.Findings)
	}
}

func TestCompareVideo2FindsPlantedErrors(t *testing.T) {
	w := loadWorkflow(t)
	b, err := os.ReadFile("../../testdata/observed2.json")
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ParseObserved(b)
	if err != nil {
		t.Fatalf("parse observed: %v", err)
	}
	rep := Compare(w, "video2-errors.mp4", observed)
	if rep.Passed {
		t.Fatal("want failed report, got passed")
	}
	want := map[[2]string]bool{
		{TypeMissing, "step-3"}:    true,
		{TypeWrongOrder, "step-4"}: true,
		{TypeMisplaced, "step-5"}:  true,
		{TypeMisplaced, "step-6"}:  true,
	}
	if got := findingPairs(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestReportFixtureMatchesCompare(t *testing.T) {
	w := loadWorkflow(t)
	obsRaw, err := os.ReadFile("../../testdata/observed2.json")
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ParseObserved(obsRaw)
	if err != nil {
		t.Fatal(err)
	}
	got := Compare(w, "video2-errors.mp4", observed)
	wantRaw, err := os.ReadFile("../../testdata/report.json")
	if err != nil {
		t.Fatal(err)
	}
	var want Report
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("parse report fixture: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("report fixture drifted from Compare output:\n%s", gotJSON)
	}
}

func TestParseObservedToleratesFences(t *testing.T) {
	b, err := os.ReadFile("../../testdata/observed2.json")
	if err != nil {
		t.Fatal(err)
	}
	obs, err := ParseObserved([]byte("```json\n" + string(b) + "\n```"))
	if err != nil {
		t.Fatalf("parse fenced: %v", err)
	}
	if len(obs) != 5 {
		t.Fatalf("want 5 observed steps, got %d", len(obs))
	}
}
