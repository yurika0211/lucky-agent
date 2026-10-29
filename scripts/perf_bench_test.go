package perf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// These benchmarks mirror the persistence shapes in #342 without importing the
// drifted module graph. They measure full-file rewrite cost, not scheduler latency.

type checkpoint struct {
	Version int               `json:"version"`
	Results map[string]string `json:"results,omitempty"`
	Digests map[string]string `json:"digests,omitempty"`
	Messages []message         `json:"messages"`
}

type message struct {
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Content    string `json:"content"`
}

func BenchmarkCheckpointTrajectory(b *testing.B) {
	for _, steps := range []int{100, 1000} {
		for _, digests := range []bool{false, true} {
			b.Run(fmt.Sprintf("operations=%d/digests=%t", steps, digests), func(b *testing.B) {
				body := strings.Repeat("output-", 146)
				var totalBytes int64
				var durations []int64
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					cp := checkpoint{Version: 1, Results: map[string]string{}, Digests: map[string]string{}}
					for i := 0; i < steps; i++ {
						id := fmt.Sprintf("operation-%d", i)
						cp.Messages = append(cp.Messages, message{Role: "tool", ToolCallID: id, Content: body})
						if digests {
							sum := sha256.Sum256([]byte(body))
							cp.Digests[id] = hex.EncodeToString(sum[:])
						} else {
							cp.Results[id] = body
						}
						start := time.Now()
						data, err := json.Marshal(cp)
						if err != nil {
							b.Fatal(err)
						}
						durations = append(durations, time.Since(start).Nanoseconds())
						totalBytes += int64(len(data))
					}
				}
				b.StopTimer()
				sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
				b.ReportMetric(float64(totalBytes)/float64(b.N), "encoded-bytes/trajectory")
				b.ReportMetric(float64(durations[(len(durations)-1)*95/100]), "encode-p95-ns")
			})
		}
	}
}

type queueState struct {
	Version int    `json:"version"`
	NextID  int64  `json:"next_id"`
	Tasks   []task `json:"tasks"`
}

type task struct {
	ID         string      `json:"id"`
	State      string      `json:"state"`
	Checkpoint []byte      `json:"checkpoint"`
	Operations []operation `json:"operations"`
}

type operation struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	State     string `json:"state"`
	Output    string `json:"output"`
}

func queueFixture(taskCount, operations int) queueState {
	state := queueState{Version: 1, NextID: int64(taskCount)}
	for i := 0; i < taskCount; i++ {
		item := task{ID: fmt.Sprintf("tq-%d", i+1), State: "in_progress", Checkpoint: []byte(`{"version":1,"messages":[{"role":"user","content":"task"}]}`)}
		for j := 0; j < operations; j++ {
			item.Operations = append(item.Operations, operation{ID: fmt.Sprintf("step-%d", j), Name: "fixture", Arguments: "{}", State: "completed", Output: strings.Repeat("output-", 146)})
		}
		state.Tasks = append(state.Tasks, item)
	}
	return state
}

func BenchmarkQueueSnapshotEncoding(b *testing.B) {
	for _, operations := range []int{100, 1000} {
		state := queueFixture(4, operations)
		for _, compact := range []bool{false, true} {
			b.Run(fmt.Sprintf("operations=%d/compact=%t", operations, compact), func(b *testing.B) {
				var encoded []byte
				var err error
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if compact {
						encoded, err = json.Marshal(state)
					} else {
						encoded, err = json.MarshalIndent(state, "", "  ")
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(encoded)), "snapshot-bytes")
			})
		}
	}
}

func BenchmarkQueuePersistLock(b *testing.B) {
	for _, operations := range []int{100, 1000} {
		b.Run(fmt.Sprintf("operations=%d", operations), func(b *testing.B) {
			state := queueFixture(4, operations)
			path := filepath.Join(b.TempDir(), "queue.json")
			durations := make([]int64, 0, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				data, err := json.MarshalIndent(state, "", "  ")
				if err != nil {
					b.Fatal(err)
				}
				err = os.WriteFile(path, data, 0o644)
				durations = append(durations, time.Since(start).Nanoseconds())
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			info, err := os.Stat(path)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size()), "snapshot-bytes")
			b.ReportMetric(float64(durations[(len(durations)-1)*95/100]), "persist-lock-p95-ns")
		})
	}
}
