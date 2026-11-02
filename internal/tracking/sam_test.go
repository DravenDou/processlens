package tracking

import (
	"math"
	"os"
	"testing"
)

// Real vectors in the Meta meta-sam conformance style:
// <frame f>id<|box;x1;y1;x2;y2;w;h|><|mask;x=0;y=0;data=W,H,[!~]payload|>,...
func TestParseSamOutputConformanceStyle(t *testing.T) {
	text := "<0f>0<|box;x1=3;y1=4;x2=7;y2=8;w=20;h=10;c=0.74|>" +
		"<|mask;x=0;y=0;c=0.74;data=5,5,!!!!!(QO(0lu8?|>" +
		",1<|box;x1=10;y1=2;x2=14;y2=6;w=20;h=10|>" +
		"<|mask;x=0;y=0;data=5,5,!!!!!(QO(0lu8?|>\n" +
		"<4f>0<|box;x1=0;y1=0;x2=4;y2=4;w=20;h=10|>" +
		"<|mask;x=0;y=0;data=5,5,!!!!!(QO(0lu8?|>\n" +
		"some prose line the model added\n"
	boxes, skipped := ParseSamOutput(text)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(boxes) != 3 {
		t.Fatalf("boxes = %d, want 3", len(boxes))
	}
	// left=x1, top=y1, right=x2+1, bottom=y2+1, normalized by w,h.
	want := SamBox{ObjectID: "0", Frame: 0, Left: 3.0 / 20, Top: 4.0 / 10, Right: 8.0 / 20, Bottom: 9.0 / 10}
	got := boxes[0]
	if got.ObjectID != want.ObjectID || got.Frame != want.Frame ||
		math.Abs(got.Left-want.Left) > 1e-9 || math.Abs(got.Top-want.Top) > 1e-9 ||
		math.Abs(got.Right-want.Right) > 1e-9 || math.Abs(got.Bottom-want.Bottom) > 1e-9 {
		t.Fatalf("box0 = %+v, want %+v", got, want)
	}
	if boxes[1].ObjectID != "1" || boxes[1].Frame != 0 {
		t.Fatalf("box1 = %+v", boxes[1])
	}
	if boxes[2].ObjectID != "0" || boxes[2].Frame != 4 {
		t.Fatalf("box2 = %+v", boxes[2])
	}
}

func TestParseSamOutputSkipsMalformed(t *testing.T) {
	cases := []string{
		"<0f>0<|box;x1=3;y1=4;x2=7;y2=8;w=20;h=10|>",                                        // mask missing
		"<0f>0<|mask;x=0;y=0;data=5,5,!!!!!x|>",                                             // box missing
		"<0f>0<|box;x1=3;y1=4;x2=7;w=20;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>",             // y2 missing
		"<0f>0<|box;x1=3;y1=4;x2=7;y2=8;w=0;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>",         // w=0
		"<0f>0<|box;x1=3;y1=4;x2=7;y2=8;w=20;h=10|><|mask;x=1;y=0;data=5,5,!!!!!x|>",        // x!=0
		"<0f>0<|box;x1=3;y1=4;x2=7;y2=8;w=20;h=10|><|mask;x=0;y=0;data=nope|>",              // bad data
		"<0f>oops-no-id<|box;x1=1;y1=1;x2=2;y2=2;w=9;h=9|><|mask;x=0;y=0;data=5,5,!!!!!x|>", // no numeric id
	}
	for i, line := range cases {
		boxes, skipped := ParseSamOutput(line + "\n")
		if len(boxes) != 0 || skipped < 1 {
			t.Fatalf("case %d: boxes=%d skipped=%d, want 0 boxes and >=1 skipped", i, len(boxes), skipped)
		}
	}
}

func TestParseSamOutputRealSample(t *testing.T) {
	// Two verbatim lines from a live sam-3.1 video response (720x1280).
	b, err := os.ReadFile("../../testdata/sam-sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	boxes, skipped := ParseSamOutput(string(b))
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(boxes) == 0 {
		t.Fatal("no boxes parsed from real sample")
	}
	first := boxes[0]
	if first.Frame != 61 || first.ObjectID != "0" {
		t.Fatalf("first = %+v", first)
	}
	close := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !close(first.Left, 120.0/720) || !close(first.Top, 488.0/1280) ||
		!close(first.Right, 444.0/720) || !close(first.Bottom, 581.0/1280) {
		t.Fatalf("first box = %+v", first)
	}
}

func TestParseSamOutputIgnoresUnknownTokens(t *testing.T) {
	text := "<2f>0<|box;x1=1;y1=1;x2=2;y2=2;w=9;h=9|>" +
		"<|future;thing=1|><|mask;x=0;y=0;data=5,5,!!!!!x|>\n"
	boxes, skipped := ParseSamOutput(text)
	if skipped != 0 || len(boxes) != 1 {
		t.Fatalf("boxes=%d skipped=%d", len(boxes), skipped)
	}
	if boxes[0].Frame != 2 {
		t.Fatalf("frame = %d", boxes[0].Frame)
	}
}
