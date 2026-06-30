package swebench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePredictionsAndRecords(t *testing.T) {
	dir := t.TempDir()
	predPath := filepath.Join(dir, "predictions.jsonl")
	if err := WritePredictions(predPath, []Prediction{{
		InstanceID:      "i1",
		ModelNameOrPath: "luckyagent/test",
		ModelPatch:      "diff --git a/a b/a\n",
	}}); err != nil {
		t.Fatalf("WritePredictions: %v", err)
	}
	predFile, err := os.Open(predPath)
	if err != nil {
		t.Fatalf("open predictions: %v", err)
	}
	defer predFile.Close()
	scanner := bufio.NewScanner(predFile)
	if !scanner.Scan() {
		t.Fatalf("expected prediction line")
	}
	var pred Prediction
	if err := json.Unmarshal(scanner.Bytes(), &pred); err != nil {
		t.Fatalf("decode prediction: %v", err)
	}
	if pred.InstanceID != "i1" {
		t.Fatalf("unexpected prediction: %#v", pred)
	}

	recordPath := filepath.Join(dir, "results.jsonl")
	records := []Record{{Type: "record", Variant: "test", InstanceID: "i1", PatchBytes: 21}}
	summary := SummarizeRecords("test", records)
	if err := WriteRecords(recordPath, records, summary); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	var count int
	scanner = bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		count++
	}
	if count != 2 {
		t.Fatalf("expected record plus summary, got %d lines", count)
	}
}
