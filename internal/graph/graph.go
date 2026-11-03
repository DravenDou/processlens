// Package graph defines the ProcessLens workflow schema: objects, steps,
// and per-step checkpoints. Pure data + validation, no network.
package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Object is a physical thing the workflow manipulates (e.g. a red 2x4 brick).
type Object struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
	Kind  string `json:"kind"`
}

// Step is one action of the workflow in execution order.
type Step struct {
	ID          string   `json:"id"`
	Index       int      `json:"index"` // 1-based position
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Objects     []string `json:"objects"` // object IDs involved in this step
}

// SnapshotItem records where one object should be after a step.
// Col/Row are a coarse 0..2 grid over the work area, Level is stack height
// (0 = resting on the mat).
type SnapshotItem struct {
	ObjectID string `json:"object_id"`
	Label    string `json:"label"`
	Col      int    `json:"col"`
	Row      int    `json:"row"`
	Level    int    `json:"level"`
}

// Checkpoint is the expected world state right after a step finishes.
type Checkpoint struct {
	StepID   string         `json:"step_id"`
	Snapshot []SnapshotItem `json:"snapshot"`
}

// Workflow is a learned physical process. Prompts remembers the tracking
// prompts Learn resolved (custom or defaults); old files without the key
// fall back to the defaults.
type Workflow struct {
	Name        string       `json:"name"`
	SourceVideo string       `json:"source_video"`
	Prompts     []string     `json:"prompts,omitempty"`
	Objects     []Object     `json:"objects"`
	Steps       []Step       `json:"steps"`
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// Validate checks internal consistency of a Workflow.
func (w *Workflow) Validate() error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is empty")
	}
	if len(w.Steps) == 0 {
		return fmt.Errorf("workflow has no steps")
	}
	knownObjects := map[string]bool{}
	for _, o := range w.Objects {
		if o.ID == "" {
			return fmt.Errorf("object with empty id")
		}
		knownObjects[o.ID] = true
	}
	seenSteps := map[string]bool{}
	for i, s := range w.Steps {
		if s.ID == "" {
			return fmt.Errorf("step %d has empty id", i)
		}
		if seenSteps[s.ID] {
			return fmt.Errorf("duplicate step id %q", s.ID)
		}
		seenSteps[s.ID] = true
		if s.Index != i+1 {
			return fmt.Errorf("step %q has index %d, want %d", s.ID, s.Index, i+1)
		}
		if s.Title == "" {
			return fmt.Errorf("step %q has empty title", s.ID)
		}
		for _, oid := range s.Objects {
			if !knownObjects[oid] {
				return fmt.Errorf("step %q references unknown object %q", s.ID, oid)
			}
		}
	}
	if len(w.Checkpoints) != len(w.Steps) {
		return fmt.Errorf("got %d checkpoints for %d steps", len(w.Checkpoints), len(w.Steps))
	}
	for _, c := range w.Checkpoints {
		if !seenSteps[c.StepID] {
			return fmt.Errorf("checkpoint references unknown step %q", c.StepID)
		}
		for _, item := range c.Snapshot {
			if !knownObjects[item.ObjectID] {
				return fmt.Errorf("checkpoint %q references unknown object %q", c.StepID, item.ObjectID)
			}
			if item.Col < 0 || item.Col > 2 || item.Row < 0 || item.Row > 2 {
				return fmt.Errorf("checkpoint %q object %q out of grid bounds", c.StepID, item.ObjectID)
			}
			if item.Level < 0 {
				return fmt.Errorf("checkpoint %q object %q has negative level", c.StepID, item.ObjectID)
			}
		}
	}
	return nil
}

// Parse decodes workflow JSON (tolerating ```json fences from LLMs) and validates it.
func Parse(b []byte) (*Workflow, error) {
	s := strings.TrimSpace(string(b))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	var w Workflow
	dec := json.NewDecoder(bytes.NewBufferString(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode workflow: %w", err)
	}
	if err := w.Validate(); err != nil {
		return nil, err
	}
	return &w, nil
}

// StepIndex maps step ID -> expected 1-based index.
func (w *Workflow) StepIndex() map[string]int {
	m := make(map[string]int, len(w.Steps))
	for _, s := range w.Steps {
		m[s.ID] = s.Index
	}
	return m
}

// CheckpointFor returns the checkpoint of a step, or nil.
func (w *Workflow) CheckpointFor(stepID string) *Checkpoint {
	for i := range w.Checkpoints {
		if w.Checkpoints[i].StepID == stepID {
			return &w.Checkpoints[i]
		}
	}
	return nil
}

// BuilderSystemPrompt is the system prompt used to turn tracked observations
// into a Workflow. The model must answer with workflow JSON only.
func BuilderSystemPrompt() string {
	return `You turn observations of a physical assembly task into a structured workflow.

Answer with a single JSON object and nothing else, matching this schema:
{"name": string, "source_video": string,
 "objects": [{"id": string, "label": string, "color": string, "kind": string}],
 "steps": [{"id": string, "index": 1-based int, "title": string,
            "description": string, "objects": [object ids]}],
 "checkpoints": [{"step_id": string,
   "snapshot": [{"object_id": string, "label": string,
                 "col": 0-2, "row": 0-2, "level": 0+ }]}]}

Rules:
- steps[] in execution order, index contiguous from 1, ids "step-1", "step-2", ...
- one checkpoint per step: the full expected object layout right after that step
- grid: the work mat seen from the fixed camera, col 0 = left, row 0 = far side
- level 0 = resting on the mat, each stacked brick +1
- name objects from their tracked labels generically: labels vary with the task, toy bricks are only one example
- never invent objects that were not observed; every referenced id must exist`
}
