package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandlers_DoNotImportBastion pins the boundary CLAUDE.md states for
// PostgreSQL ("handleServiceError ... must never grow a branch that
// knows what backs the Repository or the BlobStore") and s3_breaker.go's
// own doc comment restates for bastion: a Handler learns about a broken
// dependency only through this package's own sentinels
// (ErrDependencyUnavailable, ErrUnavailable), translated at the boundary
// by a repository or a decorator — never by importing bastion itself.
//
// A grep-shaped test rather than one exercising behaviour: the property
// under test is "this import does not appear in the source", which
// behaviour can't observe once the boundary has already been crossed —
// by the time a test could catch bastion.ErrOpenState leaking through a
// handler's response, the violation already shipped.
func TestHandlers_DoNotImportBastion(t *testing.T) {
	matches, err := filepath.Glob("../../internal/*/handler.go")
	if err != nil {
		t.Fatalf("glob handler.go files: %v", err)
	}
	// A negative assertion is satisfied by a check that never ran
	// (CLAUDE.md § Testing): confirm the glob actually found the
	// handlers this test exists to guard, rather than passing vacuously
	// because the pattern stopped matching anything.
	if len(matches) < 3 {
		t.Fatalf("glob matched %d handler.go files (%v), want at least 3 (task, user, attachment) — pattern or layout likely changed", len(matches), matches)
	}

	fset := token.NewFileSet()
	for _, path := range matches {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(importPath, "JonasBorgesLM/bastion") {
				t.Errorf("%s imports %q directly — bastion must stay behind the BlobStore/Repository boundary, translated to this package's own sentinel (ErrUnavailable, ErrDependencyUnavailable) before a handler ever sees it", path, importPath)
			}
		}
	}
}
