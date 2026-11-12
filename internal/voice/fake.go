package voice

import (
	"context"
	"sync"
)

// FakeParser is a Parser for tests and load tests: it never calls an LLM. It returns
// Results[transcript], or Err if set, or ErrCouldNotParse for an unknown transcript.
type FakeParser struct {
	Results map[string]Parsed
	Err     error

	mu    sync.Mutex
	calls []FakeCall
}

// FakeCall records one Parse call.
type FakeCall struct {
	Transcript string
	Candidates []Candidate
}

// Parse implements Parser.
func (f *FakeParser) Parse(_ context.Context, transcript string, candidates []Candidate) (Parsed, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{transcript, candidates})
	f.mu.Unlock()
	if f.Err != nil {
		return Parsed{}, f.Err
	}
	p, ok := f.Results[transcript]
	if !ok {
		return Parsed{}, ErrCouldNotParse
	}
	return p, nil
}

// Calls returns a copy of the calls so far.
func (f *FakeParser) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeCall(nil), f.calls...)
}
