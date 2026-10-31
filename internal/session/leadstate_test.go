package session

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

type countedTranscript struct {
	*os.File
	bytesRead int
}

func (r *countedTranscript) Read(p []byte) (int, error) {
	n, err := r.File.Read(p)
	r.bytesRead += n
	return n, err
}

func TestReadsOnlyTranscriptTail(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	record := "{\"type\":\"assistant\",\"message\":{\"model\":\"opus\"},\"effort\":\"high\"}\n"
	if _, err := f.WriteString(record); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(50 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	reader := &countedTranscript{File: f}
	state, err := readLeadState("claude", reader)
	if err != nil || state.Model != "" || state.Effort != "" {
		t.Fatal(state, err)
	}
	if reader.bytesRead == 0 || reader.bytesRead > 64*1024 {
		t.Fatalf("read %d bytes; budget is 65536", reader.bytesRead)
	}
	reader.bytesRead = 0
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + record); err != nil {
		t.Fatal(err)
	}
	state, err = readLeadState("claude", reader)
	if err != nil || state.Model != "opus" || state.Effort != "high" {
		t.Fatal(state, err)
	}
	if reader.bytesRead == 0 || reader.bytesRead > 64*1024 {
		t.Fatalf("read %d bytes; budget is 65536", reader.bytesRead)
	}
}
