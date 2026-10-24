package casefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/Git-Hospital/boards/schema"
)

// FileName is the name every case file has.
const FileName = "case.yaml"

var (
	compiled    *jsonschema.Schema
	compileErr  error
	compileOnce sync.Once
)

func caseSchema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.Case))
		if err != nil {
			compileErr = fmt.Errorf("embedded case schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		const url = "https://github.com/Git-Hospital/boards/schema/case.v1.json"
		if err := c.AddResource(url, doc); err != nil {
			compileErr = err
			return
		}
		compiled, compileErr = c.Compile(url)
	})
	return compiled, compileErr
}

// Load reads one case.yaml, checks it against the JSON Schema, and decodes it.
// It does not run the semantic checks; Validate does.
func Load(path string) (*Case, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// The schema is written for JSON, so the YAML goes through JSON first.
	// That also turns YAML-only forms (anchors, odd scalars) into the plain
	// values the schema reasons about.
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	asJSON, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("%s: not representable as JSON: %w", path, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(asJSON))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	sch, err := caseSchema()
	if err != nil {
		return nil, err
	}
	if err := sch.Validate(inst); err != nil {
		return nil, fmt.Errorf("%s: does not match the case schema:\n%v", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var c Case
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Absolute, because adapters run in their own work directory.
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	c.Dir = abs
	return &c, nil
}

// LoadDir loads every case.yaml under root, sorted by ID. It returns every
// load error it finds, not just the first, so one run of `boards validate`
// lists everything to fix.
func LoadDir(root string) ([]*Case, error) {
	var cases []*Case
	var errs []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != FileName {
			return nil
		}
		c, err := Load(path)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		cases = append(cases, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, c := range cases {
		if prev, ok := seen[c.ID]; ok {
			errs = append(errs, fmt.Errorf("case %s is defined twice: %s and %s", c.ID, prev, c.Dir))
		}
		seen[c.ID] = c.Dir
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, errors.Join(errs...)
}

// Select filters cases by track and by ID. An empty filter keeps everything.
// An ID that matches no case is an error, so a typo never silently runs an
// empty suite.
func Select(cases []*Case, track string, ids []string) ([]*Case, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = false
	}
	var out []*Case
	for _, c := range cases {
		if track != "" && c.Track != track {
			continue
		}
		if len(ids) > 0 {
			if _, ok := want[c.ID]; !ok {
				continue
			}
			want[c.ID] = true
		}
		out = append(out, c)
	}
	var missing []string
	for id, found := range want {
		if !found {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("no such case: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
