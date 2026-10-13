package main

import "testing"

func TestTypedCleanupResults(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.test/typed\ngo 1.26.4\n")
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.no-discarded-cleanup-error]
enabled=true
type_check=true
calls=["*.Close"]
`)
	writeFixture(t, root, "source.go", `package p
type Void struct{}
func (Void) Close() {}
type Fallible struct{}
func (Fallible) Close() error { return nil }
func f() { Void{}.Close(); Fallible{}.Close(); defer Void{}.Close(); defer Fallible{}.Close() }
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	if fixErr := lintRoot(root, options{fix: true, diff: true}, func(d diagnostic) {}); fixErr != nil {
		t.Fatal(fixErr)
	}
	writeFixture(t, root, "broken.go", "package p; var value = missing")
	if lintErr := lintRoot(root, options{}, func(d diagnostic) {}); lintErr == nil {
		t.Fatal("type errors silently accepted")
	}
}
