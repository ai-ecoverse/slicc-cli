//go:build !wasip1

package main

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
)

func startScreenProofOutput(out *jsonLines) *screen.ProofObserver {
	observer := screen.NewProofObserver()
	go func() {
		for report := range observer.Reports() {
			if !out.emitScreenProof(report) {
				return
			}
		}
	}()
	return observer
}
func (j *jsonLines) emitScreenProof(report screen.ProofReport) bool {
	if j == nil || !j.on {
		return false
	}
	b, _ := json.Marshal(report)
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false
	}
	n, e := j.w.Write(b)
	return e == nil && n == len(b)
}
