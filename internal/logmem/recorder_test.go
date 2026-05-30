package logmem

import (
	"strings"
	"testing"
)

func TestRecorderKeepsRecentEntries(t *testing.T) {
	recorder := New(2)
	_, _ = recorder.Write([]byte("one\n"))
	_, _ = recorder.Write([]byte("two\nthree\n"))

	entries := recorder.Snapshot()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Message != "two" || entries[1].Message != "three" {
		t.Fatalf("messages = %#v, want two/three", entries)
	}
	if entries[0].Seq == 0 || entries[1].Seq <= entries[0].Seq {
		t.Fatalf("sequence did not increase: %#v", entries)
	}
}

func TestRecorderIgnoresEmptyLines(t *testing.T) {
	recorder := New(10)
	_, _ = recorder.Write([]byte("\nhello\r\n\n"))

	entries := recorder.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if strings.Contains(entries[0].Message, "\r") || entries[0].Message != "hello" {
		t.Fatalf("message = %q, want hello", entries[0].Message)
	}
}
