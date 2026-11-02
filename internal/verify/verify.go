// Package verify compares an observed execution of a task against a
// learned graph.Workflow and reports missing steps, wrong order, and
// misplaced objects. Pure logic, no network.
package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"processlens/internal/graph"
)

// Finding types.
const (
	TypeMissing    = "missing"
	TypeWrongOrder = "wrong-order"
	TypeMisplaced  = "misplaced"
)

// ObservedStep is one step detected in a new execution, in observed order.
type ObservedStep struct {
	StepID   string               `json:"step_id"` // matched expected step, "" if unknown
	TSeconds float64              `json:"t_seconds"`
	Snapshot []graph.SnapshotItem `json:"snapshot"`
}

// Finding is one deviation from the expected workflow.
type Finding struct {
	Type     string  `json:"type"`
	StepID   string  `json:"step_id"`
	Detail   string  `json:"detail"`
	TSeconds float64 `json:"t_seconds,omitempty"`
}

// Report is the result of verifying one execution.
type Report struct {
	WorkflowName string    `json:"workflow_name"`
	SourceVideo  string    `json:"source_video"`
	Passed       bool      `json:"passed"`
	Findings     []Finding `json:"findings"`
}

// ParseObserved decodes an observed-step list (tolerating ```json fences).
func ParseObserved(b []byte) ([]ObservedStep, error) {
	s := strings.TrimSpace(string(b))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	var obs []ObservedStep
	dec := json.NewDecoder(bytes.NewBufferString(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obs); err != nil {
		return nil, fmt.Errorf("decode observed steps: %w", err)
	}
	return obs, nil
}

// Compare aligns observed steps with the workflow and builds a Report.
func Compare(w *graph.Workflow, sourceVideo string, observed []ObservedStep) Report {
	rep := Report{WorkflowName: w.Name, SourceVideo: sourceVideo, Findings: []Finding{}}
	expectedIdx := w.StepIndex()
	seen := map[string]ObservedStep{}

	// Wrong order: walk observed order, flag any step whose expected index
	// goes backwards relative to the max seen so far.
	maxIdx := 0
	for _, o := range observed {
		if o.StepID == "" {
			continue // unknown extra action: not scored in MVP
		}
		idx, ok := expectedIdx[o.StepID]
		if !ok {
			continue // references no known step: not scored in MVP
		}
		seen[o.StepID] = o
		if idx < maxIdx {
			rep.Findings = append(rep.Findings, Finding{
				Type:     TypeWrongOrder,
				StepID:   o.StepID,
				Detail:   fmt.Sprintf("step %q executed out of order (expected position %d)", o.StepID, idx),
				TSeconds: o.TSeconds,
			})
		} else {
			maxIdx = idx
		}
	}

	// Missing: expected steps never observed.
	for _, s := range w.Steps {
		if _, ok := seen[s.ID]; !ok {
			rep.Findings = append(rep.Findings, Finding{
				Type:   TypeMissing,
				StepID: s.ID,
				Detail: fmt.Sprintf("step %d %q was not executed", s.Index, s.Title),
			})
		}
	}

	// Misplaced: matched steps whose own objects differ from the checkpoint.
	// Only the step's own objects are checked, so one error does not cascade
	// into every later step; full snapshots stay in the data for review.
	for _, s := range w.Steps {
		o, ok := seen[s.ID]
		if !ok {
			continue
		}
		cp := w.CheckpointFor(s.ID)
		if cp == nil {
			continue
		}
		own := map[string]bool{}
		for _, oid := range s.Objects {
			own[oid] = true
		}
		want := map[string]graph.SnapshotItem{}
		for _, item := range cp.Snapshot {
			if own[item.ObjectID] {
				want[item.ObjectID] = item
			}
		}
		got := map[string]graph.SnapshotItem{}
		for _, item := range o.Snapshot {
			got[item.ObjectID] = item
		}
		for oid, witem := range want {
			gitem, ok := got[oid]
			if !ok {
				rep.Findings = append(rep.Findings, Finding{
					Type:     TypeMisplaced,
					StepID:   s.ID,
					Detail:   fmt.Sprintf("object %q absent after step %q", oid, s.ID),
					TSeconds: o.TSeconds,
				})
				continue
			}
			if gitem.Col != witem.Col || gitem.Row != witem.Row || gitem.Level != witem.Level {
				rep.Findings = append(rep.Findings, Finding{
					Type:   TypeMisplaced,
					StepID: s.ID,
					Detail: fmt.Sprintf("object %q at (%d,%d,L%d), expected (%d,%d,L%d)",
						oid, gitem.Col, gitem.Row, gitem.Level, witem.Col, witem.Row, witem.Level),
					TSeconds: o.TSeconds,
				})
			}
		}
	}

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].Type != rep.Findings[j].Type {
			return rep.Findings[i].Type < rep.Findings[j].Type
		}
		return rep.Findings[i].StepID < rep.Findings[j].StepID
	})
	rep.Passed = len(rep.Findings) == 0
	return rep
}

// DescribeSystemPrompt asks the model to map a new execution (tracks + notes)
// to observed steps referencing the workflow's step IDs.
func DescribeSystemPrompt() string {
	return `You watch a new execution of a known assembly workflow and map what happened.

Answer with a single JSON array and nothing else:
[{"step_id": "step-N from the workflow (or \"\" if unmatchable)",
  "t_seconds": number,
  "snapshot": [{"object_id": string, "label": string,
                "col": 0-2, "row": 0-2, "level": 0+}]}]

Rules:
- one entry per detected step, in observed order
- snapshot = full object layout right after that step, same grid as the workflow
- use "" step_id only for actions matching no known step`
}
