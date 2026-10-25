package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/dop251/goja"
)

const (
	maxClaims       = 32
	maxReflexes     = 16
	maxSourceBytes  = 8 << 10
	maxLibraryBytes = 2 << 20
)

// Program is the executable definition and qualification of a Reflex. Its JSON
// determines its content identity; provenance and retirement never enter that identity.
type Program struct {
	Parameters json.RawMessage           `json:"parameters_schema,omitempty"`
	Steps      map[string]StepDefinition `json:"steps,omitempty"`
	Proof      *ReflexQualification      `json:"verification,omitempty"`
	When       string                    `json:"when"`
	Decide     string                    `json:"decide"`
	Observe    string                    `json:"observe"`
	Readers    map[string]string         `json:"readers,omitempty"`
	program    *goja.Program
	arguments  map[string]any // Compilation evidence only; never persisted.
}

type claimRecord struct {
	Claim jevapi.Claim `json:"claim"`
	Task  string       `json:"task"`
}

type Reflex struct {
	Program
	Claims        []string          `json:"claims"`
	Contracts     map[string]string `json:"contracts,omitempty"`
	Archived      bool              `json:"archived,omitempty"`
	ArchiveReason string            `json:"archive_reason,omitempty"`
}
type library struct {
	Claims   map[string]claimRecord `json:"claims"`
	Reflexes map[string]Reflex      `json:"reflexes"`
}

func (r *Program) validate() error {
	if strings.TrimSpace(r.When) == "" || strings.TrimSpace(r.Decide) == "" || len(r.When)+len(r.Decide) > 8192 || strings.TrimSpace(r.Observe) == "" || len(r.Observe) > 16<<10 {
		return errors.New("invalid Reflex scene")
	}
	r.program = nil
	for id, source := range r.Readers {
		if strings.TrimSpace(id) == "" || len(id) > 64 {
			return errors.New("reader: invalid reader identifier")
		}
		if err := validateReader(id, source); err != nil {
			return err
		}
	}
	code := strings.TrimSpace(r.Observe)
	if !strings.HasPrefix(code, "js:") {
		return errors.New("Observe must be a js: JavaScript program")
	}
	var err error
	source := strings.TrimSpace(strings.TrimPrefix(code, "js:"))
	if err := validateReader("reflex", source); err != nil {
		return fmt.Errorf("reflex must be a function(context, arguments): %w", err)
	}
	r.program, err = goja.Compile("reflex", "("+source+")", true)
	if err != nil {
		return fmt.Errorf("observe syntax: %w", err)
	}
	return nil
}
