package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.package-boundaries]
enabled=true
include_tests=true
[[rules.package-boundaries.boundaries]]
name="storage independent"
from=["storage"]
exclude_dirs=["storage/compat"]
deny_imports=["example.test/repo/virt"]
allow_imports=["example.test/repo/virt/contract"]
`)
	writeFixture(t, root, "storage/value.go", `package storage
import (
 backend "example.test/repo/virt/backend"
 _ "example.test/repo/virt"
 . "example.test/repo/virt/other"
 "example.test/repo/virtual"
 "example.test/repo/virt/contract"
 //repolint:ignore package-boundaries -- audited compatibility boundary
 "example.test/repo/virt/legacy"
 "example.test/repo/virt/contract/subpackage"
)
`)
	writeFixture(t, root, "storage/value_test.go", `package storage; import _ "example.test/repo/virt/backend"`)
	writeFixture(t, root, "storage/compat/value.go", `package compat; import _ "example.test/repo/virt/backend"`)
	writeFixture(t, root, "storage-other/value.go", `package other; import _ "example.test/repo/virt/backend"`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 5 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	for _, d := range diagnostics {
		if d.Rule != rulePackageBoundaries || !strings.Contains(d.Message, "storage independent") || d.End.Offset <= d.Position.Offset {
			t.Fatalf("diagnostic: %+v", d)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--quiet", filepath.Join(root, "storage")}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
}

func TestBoundaryConfigValidation(t *testing.T) {
	for _, body := range []string{
		`name="";from=["storage"];deny_imports=["os"]`,
		`name="x";from=["../storage"];deny_imports=["os"]`,
		`name="x";from=["storage/**"];deny_imports=["os"]`,
		`name="x";from=["storage"];deny_imports=["os/**"]`,
		`name="x";from=["storage"];deny_imports=[]`,
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, ".repolint.toml", "schema=1\n[rules.package-boundaries]\nenabled=true\n[[rules.package-boundaries.boundaries]]\n"+strings.ReplaceAll(body, ";", "\n"))
			if _, _, configErr := loadConfig(root, ""); configErr == nil {
				t.Fatal("invalid boundary accepted")
			}
		})
	}
}
