package main

import "testing"

func TestSuppressionAudit(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.suppression-policy]
enabled=true
`)
	writeFixture(t, root, "source.go", `package p
//repolint:ignore no-any -- external boundary
var valid any
//repolint:ignore no-any
var unexplained any
//repolint:ignore no-any -- obsolete
var ordinary int
//repolint:ignore misspelled -- external boundary
var unknown int
//repolint:ignore all -- blanket
var blanket any
//repolint:ignore
var malformed int
//repolint:ignore suppression-policy -- bypass
var bypass int
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 6 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	for _, d := range diagnostics {
		if d.Rule != ruleSuppression {
			t.Fatalf("unexpected diagnostic: %+v", d)
		}
	}
}
