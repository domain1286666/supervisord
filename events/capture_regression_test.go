package events

import (
	"strings"
	"testing"
)

func TestCaptureSkipsStrayEndAndOversizedFrames(t *testing.T) {
	for _, prefix := range []string{
		ProcCommonEndStr + "noise",
		ProcCommonBeginStr + strings.Repeat("x", 65) + ProcCommonEndStr,
	} {
		capture := &ProcCommEventCapture{
			captureMaxBytes: 64,
			eventBuffer:     prefix + ProcCommonBeginStr + "valid" + ProcCommonEndStr,
			eventBeginPos:   -1,
		}
		event := capture.captureEvent()
		if event == nil || !strings.HasSuffix(event.GetBody(), "\nvalid") {
			t.Fatalf("capture after %q = %v; want the valid frame", prefix, event)
		}
		if extra := capture.captureEvent(); extra != nil {
			t.Fatalf("unexpected duplicate frame: %v", extra)
		}
	}
}

func FuzzCaptureEvent(f *testing.F) {
	f.Add(ProcCommonEndStr + ProcCommonBeginStr + "ok" + ProcCommonEndStr)
	f.Add(ProcCommonBeginStr + "incomplete")
	f.Fuzz(func(t *testing.T, input string) {
		capture := &ProcCommEventCapture{captureMaxBytes: 1024, eventBuffer: input, eventBeginPos: -1}
		for event := capture.captureEvent(); event != nil; event = capture.captureEvent() {
			if len(event.(*ProcCommEvent).data) > 1024 {
				t.Fatal("captured oversized frame")
			}
		}
		if len(capture.eventBuffer) > 1024+len(ProcCommonBeginStr)+len(ProcCommonEndStr) {
			t.Fatal("incomplete frame retention is unbounded")
		}
	})
}
