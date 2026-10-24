package testbed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// A patch in the kubernetes-yaml format is applied the way an operator
// would apply it: `kubectl apply` of each document, which creates the
// object or merges into the one of the same kind, namespace and name
// (architecture §9, question 3). A patch that names a different object than
// the faulty one is therefore applied as it is, and judged by whether it
// heals. Before applying, the current state of every object it names is
// saved, so that removing the patch restores exactly what was there.

// saved is one object as it was before a patch: its document, and whether
// it existed at all.
type saved struct {
	doc     string // the patch's document, to delete what it created
	before  string // the object before, ready for kubectl replace; "" if absent
	existed bool
}

// splitDocs splits a multi-document YAML stream into the documents that
// carry an object, dropping empty ones.
func splitDocs(content string) ([]string, error) {
	dec := yaml.NewDecoder(strings.NewReader(content))
	var docs []string
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("patch: %w", err)
		}
		if len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
			continue
		}
		var m map[string]any
		if err := n.Decode(&m); err != nil {
			return nil, fmt.Errorf("patch: %w", err)
		}
		meta, _ := m["metadata"].(map[string]any)
		if m["kind"] == nil || m["apiVersion"] == nil || meta == nil || meta["name"] == nil {
			return nil, fmt.Errorf("patch: every document needs apiVersion, kind and metadata.name")
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		if err := enc.Encode(&n); err != nil {
			return nil, err
		}
		docs = append(docs, buf.String())
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("patch: no objects")
	}
	return docs, nil
}

// restorable strips the fields the server owns from an object read back
// with `kubectl get -f - -o yaml`, so that `kubectl replace` puts back its
// spec and metadata without tripping on a stale resourceVersion. kubectl
// prints a List even for one document; the object is its only item.
func restorable(objYAML string) (string, error) {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(objYAML), &m); err != nil {
		return "", err
	}
	if m["kind"] == "List" {
		items, _ := m["items"].([]any)
		if len(items) != 1 {
			return "", fmt.Errorf("patch: expected one object, kubectl returned %d", len(items))
		}
		if m, _ = items[0].(map[string]any); m == nil {
			return "", fmt.Errorf("patch: kubectl returned an item that is not an object")
		}
	}
	delete(m, "status")
	if meta, ok := m["metadata"].(map[string]any); ok {
		for _, k := range []string{"resourceVersion", "uid", "creationTimestamp", "generation", "managedFields", "selfLink"} {
			delete(meta, k)
		}
	}
	out, err := yaml.Marshal(m)
	return string(out), err
}

// kubectlStdin runs kubectl with a document on stdin.
type kubectlStdin func(ctx context.Context, stdin string, args ...string) (stdout, stderr string, code int, err error)

// applyPatch saves the state of every object the patch names, then applies
// it. The saved state is returned even when applying fails part way, so
// that removing the patch can undo what did go in.
func applyPatch(ctx context.Context, kc kubectlStdin, content string) ([]saved, error) {
	docs, err := splitDocs(content)
	if err != nil {
		return nil, err
	}
	var out []saved
	for _, d := range docs {
		stdout, stderr, code, err := kc(ctx, d, "get", "-f", "-", "-o", "yaml")
		if err != nil {
			return out, err
		}
		s := saved{doc: d}
		switch {
		case code == 0:
			if s.before, err = restorable(stdout); err != nil {
				return out, err
			}
			s.existed = true
		case strings.Contains(stderr, "NotFound"):
		default:
			return out, fmt.Errorf("patch: reading the current object: %s", strings.TrimSpace(stderr))
		}
		out = append(out, s)
	}
	for _, d := range docs {
		_, stderr, code, err := kc(ctx, d, "apply", "-f", "-")
		if err != nil {
			return out, err
		}
		if code != 0 {
			return out, fmt.Errorf("patch: kubectl apply: %s", strings.TrimSpace(stderr))
		}
	}
	return out, nil
}

// removePatch puts back every saved object, in reverse order: replaced if
// it existed, deleted if the patch created it.
func removePatch(ctx context.Context, kc kubectlStdin, objs []saved) error {
	var errs []error
	for i := len(objs) - 1; i >= 0; i-- {
		s := objs[i]
		var stderr string
		var code int
		var err error
		if s.existed {
			_, stderr, code, err = kc(ctx, s.before, "replace", "-f", "-")
		} else {
			_, stderr, code, err = kc(ctx, s.doc, "delete", "-f", "-", "--ignore-not-found")
		}
		if err == nil && code != 0 {
			err = fmt.Errorf("%s", strings.TrimSpace(stderr))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("patch: restoring object %d: %w", i, err))
		}
	}
	return errors.Join(errs...)
}
