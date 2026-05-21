package ci_test

import (
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow/ci"
)

// spyObserver captures every Observer call so assertions can pin the
// exact source_type / signal / risk label combinations and audit
// Extra-field maps that real production wiring produces.
type spyObserver struct {
	emits        []emitCall
	audits       []audit.SIEMEvent
	oidcOutcomes []oidcOutcomeCall
}

type emitCall struct {
	Provider ci.Provider
	Signal   string
	Risk     string
}

type oidcOutcomeCall struct {
	Provider ci.Provider
	Result   string
}

func newSpyObserver() *spyObserver { return &spyObserver{} }

func (s *spyObserver) RecordFindingEmit(provider ci.Provider, signal, risk string) {
	s.emits = append(s.emits, emitCall{Provider: provider, Signal: signal, Risk: risk})
}

func (s *spyObserver) EmitAudit(event audit.SIEMEvent) {
	s.audits = append(s.audits, event)
}

func (s *spyObserver) OIDCVerifyOutcome(provider ci.Provider, result string) {
	s.oidcOutcomes = append(s.oidcOutcomes, oidcOutcomeCall{Provider: provider, Result: result})
}
