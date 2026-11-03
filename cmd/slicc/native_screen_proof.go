//go:build !wasip1

package main

import (
	"encoding/json"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/screen"
)

func startScreenProofOutput(out *jsonLines) (*screen.ProofObserver, func()) {
	observer := screen.NewProofObserver()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for report := range observer.Reports() {
			if !out.emitScreenProof(report) {
				return
			}
		}
	}()
	return observer, func() { observer.Close(); <-done }
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
