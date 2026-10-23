package testbed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LockSchema is the testbed.lock schema this build reads.
const LockSchema = "boards.testbed/v1"

// Lock is testbed/testbed.lock: the one pinned testbed (D18).
type Lock struct {
	SchemaVersion string `yaml:"schemaVersion"`
	Kind          struct {
		Version   string `yaml:"version"`
		NodeImage string `yaml:"nodeImage"`
	} `yaml:"kind"`
	Istio struct {
		Version string `yaml:"version"`
		Profile string `yaml:"profile"`
	} `yaml:"istio"`
	Manifests map[string]Manifest `yaml:"manifests"`

	// Dir is the directory holding the lock; manifest paths are relative to it.
	Dir string `yaml:"-"`
	// Digest is the SHA-256 of the lock file. Since the lock carries the
	// hash of every manifest, it stands for the whole testbed.
	Digest string `yaml:"-"`
}

// Manifest is one file the testbed applies.
type Manifest struct {
	Path   string `yaml:"path"`
	Source string `yaml:"source"`
	SHA256 string `yaml:"sha256"`
}

// LoadLock reads a lock and checks every manifest against its hash. A
// manifest that does not match is a different testbed, so it is an error
// rather than a quiet change.
func LoadLock(path string) (*Lock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var l Lock
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if l.SchemaVersion != LockSchema {
		return nil, fmt.Errorf("%s: schemaVersion %q, this build reads %q", path, l.SchemaVersion, LockSchema)
	}
	if l.Kind.NodeImage == "" || l.Istio.Version == "" {
		return nil, fmt.Errorf("%s: kind.nodeImage and istio.version are required", path)
	}
	if l.Dir, err = filepath.Abs(filepath.Dir(path)); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	l.Digest = hex.EncodeToString(sum[:])

	var errs []error
	for name, m := range l.Manifests {
		got, err := fileSHA256(filepath.Join(l.Dir, m.Path))
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("manifest %s: %w", name, err))
		case got != m.SHA256:
			errs = append(errs, fmt.Errorf("manifest %s (%s) has sha256 %s, the lock says %s; update the lock if the change is meant", name, m.Path, got, m.SHA256))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

// File returns the absolute path of the named manifest.
func (l *Lock) File(name string) (string, error) {
	m, ok := l.Manifests[name]
	if !ok {
		return "", fmt.Errorf("testbed.lock has no manifest %q", name)
	}
	return filepath.Join(l.Dir, m.Path), nil
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
