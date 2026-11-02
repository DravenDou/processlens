// Package tracking parses the hosted SAM 3.1 record grammar.
//
// Wire format (Meta meta-sam protocol, see conformance cases):
//
//	line    := "<" frame "f" [";" fields] ">" records
//	records := record ("," record)*
//	record  := objectId token+          (exactly one box + one mask token)
//	box     := "<|box;x1=I;y1=I;x2=I;y2=I;w=U;h=U[;c=C]|>"
//	mask    := "<|mask;x=0;y=0;data=W,H,[!~]payload[;c=C]|>"
//
// Coordinates are half-open: left=x1, top=y1, right=x2+1, bottom=y2+1,
// normalized by the media dimensions w,h carried in every box token.
// Mask payloads are validated and ignored: ProcessLens only needs boxes.
// Lines that are not frame headers (prose) and malformed records are skipped.
package tracking

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	samHeader  = regexp.MustCompile(`^<([0-9]+)f(?:;[^>]*)?>(.*)$`)
	samToken   = regexp.MustCompile(`<\|([A-Za-z][A-Za-z0-9_.-]*)(?:;([^|]*))?\|>`)
	samMask    = regexp.MustCompile(`^([0-9]+),([0-9]+),([!~][^|]+)$`)
	samRecID   = regexp.MustCompile(`^\s*,?\s*([0-9]+)\s*$`)
	samBetween = regexp.MustCompile(`^\s*(?:,?\s*([0-9]+)\s*)?$`)
)

const maxSafeInt = 1<<53 - 1

// SamBox is one parsed box record with normalized 0..1 coordinates.
type SamBox struct {
	ObjectID string
	Frame    int
	Left     float64
	Top      float64
	Right    float64
	Bottom   float64
}

// ParseSamOutput parses model output text into box records.
// It returns the boxes and the count of skipped malformed records.
func ParseSamOutput(text string) (boxes []SamBox, skipped int) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := samHeader.FindStringSubmatch(line)
		if m == nil {
			continue // prose line, not a frame header
		}
		frame, err := samInt(m[1], false)
		if err != nil || frame < 0 {
			continue
		}
		b, s := parseFrameBody(frame, m[2])
		boxes = append(boxes, b...)
		skipped += s
	}
	return boxes, skipped
}

type samTok struct {
	name  string
	body  string
	start int
	end   int
}

func parseFrameBody(frame int, body string) ([]SamBox, int) {
	idx := samToken.FindAllStringSubmatchIndex(body, -1)
	if len(idx) == 0 {
		return nil, 0
	}
	toks := make([]samTok, 0, len(idx))
	for _, m := range idx {
		name := body[m[2]:m[3]]
		fields := ""
		if m[4] >= 0 {
			fields = body[m[4]:m[5]]
		}
		toks = append(toks, samTok{name: name, body: fields, start: m[0], end: m[1]})
	}
	// Split tokens into records on `<id>` boundaries between them.
	var out []SamBox
	skipped := 0
	start := 0
	id := ""
	if m := samRecID.FindStringSubmatch(body[:toks[0].start]); m == nil {
		return nil, 1 // no leading record id: skip the line
	} else {
		id = m[1]
	}
	flush := func(id string, group []samTok) {
		b, ok := buildBox(frame, id, group)
		if !ok {
			skipped++
			return
		}
		out = append(out, b)
	}
	for i := 1; i < len(toks); i++ {
		between := body[toks[i-1].end:toks[i].start]
		m := samBetween.FindStringSubmatch(between)
		if m == nil {
			skipped++ // cannot resync: abandon the rest of the line
			return out, skipped
		}
		if m[1] != "" {
			flush(id, toks[start:i])
			start = i
			id = m[1]
		}
	}
	if rest := strings.TrimSpace(body[toks[len(toks)-1].end:]); rest != "" {
		skipped++
		return out, skipped
	}
	flush(id, toks[start:])
	return out, skipped
}

func samFields(body string) map[string]string {
	fields := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return fields
}

func samInt(s string, unsigned bool) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	if n < 0 && unsigned {
		return 0, fmt.Errorf("negative unsigned")
	}
	if n > maxSafeInt || n < -maxSafeInt {
		return 0, fmt.Errorf("out of range")
	}
	return n, nil
}

func buildBox(frame int, id string, group []samTok) (SamBox, bool) {
	var box, mask *samTok
	for i := range group {
		switch group[i].name {
		case "box":
			if box != nil {
				return SamBox{}, false
			}
			box = &group[i]
		case "mask":
			if mask != nil {
				return SamBox{}, false
			}
			mask = &group[i]
		}
	}
	if box == nil || mask == nil {
		return SamBox{}, false
	}
	bf := samFields(box.body)
	x1, err1 := samInt(bf["x1"], false)
	y1, err2 := samInt(bf["y1"], false)
	x2, err3 := samInt(bf["x2"], false)
	y2, err4 := samInt(bf["y2"], false)
	w, err5 := samInt(bf["w"], true)
	h, err6 := samInt(bf["h"], true)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil || w <= 0 || h <= 0 {
		return SamBox{}, false
	}
	mf := samFields(mask.body)
	if mf["x"] != "0" || mf["y"] != "0" {
		return SamBox{}, false
	}
	if !samMask.MatchString(mf["data"]) {
		return SamBox{}, false
	}
	return SamBox{
		ObjectID: id,
		Frame:    frame,
		Left:     clamp(float64(x1) / float64(w)),
		Top:      clamp(float64(y1) / float64(h)),
		Right:    clamp(float64(x2+1) / float64(w)),
		Bottom:   clamp(float64(y2+1) / float64(h)),
	}, true
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
