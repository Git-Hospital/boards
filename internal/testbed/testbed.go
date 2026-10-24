// Package testbed is where cases run: a kind cluster with a mesh, a bare
// cluster for charts, or a fixture checkout. This file holds the interface
// and the fake used by dry runs; the real testbeds live beside it.
package testbed

import (
	"context"
	"slices"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// State is the answer of an oracle. NoData is its own state: an empty query
// result is neither "the defect is live" nor "the defect is gone", and it
// never counts as healed (D12).
type State string

const (
	Holds  State = "holds"
	Absent State = "absent"
	NoData State = "no-data"
)

// Info describes a testbed for the report.
type Info struct {
	Name string `json:"name"`
	// Standard is false for anything but the pinned testbed.lock: another
	// Istio version, a reused cluster, a dry run. Such results never enter
	// the official table (D18).
	Standard   bool   `json:"standard"`
	LockDigest string `json:"lockDigest,omitempty"`
}

// Testbed runs the steps of a case and answers its oracles.
type Testbed interface {
	Info() Info
	// Env is the environment every command and adapter gets: the private
	// kubeconfig, a scratch HOME, the Prometheus address.
	Env() []string
	// Quiesce waits for residual signals of an earlier case to decay.
	Quiesce(ctx context.Context, d time.Duration) error
	// Inject sets up one twin: the defect, the clean twin, or a control's noise.
	Inject(ctx context.Context, c *casefile.Case, twin casefile.Twin) error
	// Reset undoes Inject for the same twin. It must be safe to run at any time.
	Reset(ctx context.Context, c *casefile.Case, twin casefile.Twin) error
	// Defect evaluates the case's own oracle.
	Defect(ctx context.Context, c *casefile.Case) (State, error)
	// Check evaluates one clean-twin check.
	Check(ctx context.Context, c *casefile.Case, o casefile.Oracle) (State, error)
	ApplyPatch(ctx context.Context, c *casefile.Case, p *verdict.Patch) error
	RemovePatch(ctx context.Context, c *casefile.Case, p *verdict.Patch) error
	// Inventory lists the cluster's objects, to measure what a tool created
	// or deleted while it investigated.
	Inventory(ctx context.Context) ([]string, error)
	Close(ctx context.Context) error
}

// Fake is the dry-run testbed. It touches nothing: it pretends that every
// inject step takes effect at once, and that a patch heals exactly when its
// class is one the case accepts. A dry run therefore tests boards' own
// lifecycle and scoring, never a tool (D32).
type Fake struct {
	env []string

	defectLive bool
	healed     bool
	harmed     bool
}

// NewFake returns a dry-run testbed. env is passed to adapters unchanged.
func NewFake(env []string) *Fake { return &Fake{env: env} }

func (f *Fake) Info() Info    { return Info{Name: "dry-run", Standard: false} }
func (f *Fake) Env() []string { return f.env }

func (f *Fake) Quiesce(context.Context, time.Duration) error { return nil }

// Inject makes the defect live for a fault case's defect twin. The clean
// twin and a control's noise change nothing the oracle looks at.
func (f *Fake) Inject(_ context.Context, c *casefile.Case, twin casefile.Twin) error {
	f.defectLive = twin == casefile.TwinDefect && c.Kind == casefile.KindFault
	f.healed, f.harmed = false, false
	return nil
}

func (f *Fake) Reset(context.Context, *casefile.Case, casefile.Twin) error {
	f.defectLive, f.healed, f.harmed = false, false, false
	return nil
}

// Defect holds while the defect is live and unhealed. A control case's
// condition (the noise is in place, the service healthy) always holds.
func (f *Fake) Defect(_ context.Context, c *casefile.Case) (State, error) {
	if c.Kind == casefile.KindControl || (f.defectLive && !f.healed) {
		return Holds, nil
	}
	return Absent, nil
}

// Check reports the clean-twin checks as passing unless a forbidden patch
// was applied.
func (f *Fake) Check(context.Context, *casefile.Case, casefile.Oracle) (State, error) {
	if f.harmed {
		return Absent, nil
	}
	return Holds, nil
}

func (f *Fake) ApplyPatch(_ context.Context, c *casefile.Case, p *verdict.Patch) error {
	r := c.Expect.Remediation
	if r == nil {
		return nil
	}
	f.healed = slices.Contains(r.Accept, p.Class)
	f.harmed = slices.Contains(r.Forbid, p.Class)
	return nil
}

func (f *Fake) RemovePatch(context.Context, *casefile.Case, *verdict.Patch) error {
	f.healed, f.harmed = false, false
	return nil
}

func (f *Fake) Inventory(context.Context) ([]string, error) { return nil, nil }
func (f *Fake) Close(context.Context) error                 { return nil }
