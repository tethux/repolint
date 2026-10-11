package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIOutputAndExitCodes(t *testing.T) {
	root := t.TempDir()
	path := writeFixture(t, root, "source.go", "package p; var value any")
	var stdout, stderr bytes.Buffer
	if status := run([]string{root}, &stdout, &stderr); status != 1 {
		t.Fatalf("exit = %d", status)
	}
	if !strings.Contains(stderr.String(), "source.go:1:22: no-any:") || !strings.Contains(stderr.String(), "repolint: 1 issue in 1 file") || stdout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "[no-any]") || strings.Contains(stderr.String(), "  fix:") {
		t.Fatal("old diagnostic format remains")
	}
	writeFixture(t, root, "source.go", "package p; var value int")
	stderr.Reset()
	if status := run([]string{"--quiet", path}, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet: exit=%d output=%q", status, stderr.String())
	}
	if status := run([]string{"--version"}, &stdout, &stderr); status != 0 || stdout.String() != "repolint 0.2.0\n" {
		t.Fatalf("version: exit=%d output=%q", status, stdout.String())
	}
	stderr.Reset()
	if status := run([]string{"--diff", root}, &stdout, &stderr); status != 2 {
		t.Fatalf("invalid flags: exit=%d", status)
	}
	if !strings.Contains(stderr.String(), "require --fix") {
		t.Fatal(stderr.String())
	}
}

func TestCLIDiffDoesNotMixDiagnosticsIntoPatch(t *testing.T) {
	root := newFixtureModule(t)
	path := writeFixture(t, root, "bridge/source.go", "package bridge; import \"errors\"; func f() error {return errors.New(\"closed\")}")
	before := readFixture(t, path)
	var stdout, stderr bytes.Buffer
	if status := run([]string{"--fix", "--diff", "--allow-structural", root}, &stdout, &stderr); status != 0 {
		t.Fatalf("exit=%d stderr=%s", status, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "--- /dev/null") || strings.Contains(stdout.String(), "repolint:") || !strings.Contains(stderr.String(), "would fix 2 files") {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if before != readFixture(t, path) {
		t.Fatal("preview changed source")
	}
	if _, _, exists := strings.Cut(stdout.String(), "bridge/source.go"); !exists {
		t.Fatal("source diff is missing")
	}
}

func TestDiffSeparatesDistantHunks(t *testing.T) {
	old := strings.Repeat("unchanged\n", 20)
	next := strings.Replace(old, "unchanged\n", "first\n", 1)
	next = strings.TrimSuffix(next, "unchanged\n") + "last\n"
	patch := unifiedDiff(fileEdit{Path: filepath.FromSlash("src/file.go"), Before: []byte(old), After: []byte(next), Exists: true})
	if strings.Count(patch, "@@ -") != 2 || strings.Contains(patch, strings.Repeat(" unchanged\n", 7)) {
		t.Fatalf("patch is not compact:\n%s", patch)
	}
}

func TestDiffHandlesEmptyFilesAndMissingNewlines(t *testing.T) {
	patch := unifiedDiff(fileEdit{Path: "new.go", After: []byte("package p\n")})
	if patch != "--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,1 @@\n+package p\n" {
		t.Fatal(patch)
	}
	patch = unifiedDiff(fileEdit{Path: "source.go", Before: []byte("package p"), After: []byte("package p\n"), Exists: true})
	if !strings.Contains(patch, "-package p\n\\ No newline at end of file\n+package p\n") {
		t.Fatal(patch)
	}
}
