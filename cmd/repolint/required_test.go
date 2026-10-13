package main

import (
	"bytes"
	"testing"
)

func TestRequiredRulesCannotBeDisabled(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "source.go", "package p; var number int")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--require-rules=no-any", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("valid rule rejected: %s", stderr.String())
	}
	for _, config := range []string{"[rules.no-any]\nenabled=false", "[rules.no-any]\npaths=[]"} {
		writeFixture(t, root, ".repolint.toml", "schema=1\n"+config)
		if code := run([]string{"--require-rules=no-any", root}, &stdout, &stderr); code != 2 {
			t.Fatalf("disabled rule accepted: %d", code)
		}
	}
}
