package main

import (
	"bytes"
	"go/token"
	"strings"
	"testing"
)

func TestDiagnosticSpanAlignment(t *testing.T) {
	renderer := diagnosticRenderer{}
	d := diagnostic{
		Rule: ruleStructuredError, Message: "use a structured error",
		Position:   token.Position{Filename: "source.go", Line: 31, Column: 10},
		End:        token.Position{Line: 31, Column: 21},
		SourceLine: "\t\treturn fmt.Errorf(\"closed\")",
	}
	output := renderer.render(d)
	if !strings.Contains(output, "31 |         return fmt.Errorf(\"closed\")") || !strings.Contains(output, "   |                ^^^^^^^^^^^") {
		t.Fatalf("misaligned tab span:\n%s", output)
	}
	d.Position.Column = 7
	d.End.Column = 10
	d.SourceLine = "// é any"
	if output = renderer.render(d); !strings.Contains(output, "   |      ^^^") {
		t.Fatalf("misaligned UTF-8 span:\n%s", output)
	}
}

func TestDiagnosticColorsAndFixHint(t *testing.T) {
	root := newFixtureModule(t)
	writeFixture(t, root, "bridge/source.go", "package bridge\nimport \"errors\"\nfunc f() error { return errors.New(\"closed\") }\n")
	for _, mode := range []string{"always", "never"} {
		var stdout, stderr bytes.Buffer
		if status := run([]string{"--color=" + mode, root}, &stdout, &stderr); status != 1 {
			t.Fatalf("exit=%d output=%s", status, stderr.String())
		}
		output := stderr.String()
		if strings.Contains(output, "\x1b[") != (mode == "always") {
			t.Fatalf("color=%s output=%q", mode, output)
		}
		for _, expected := range []string{"error[structured-error]", "source.go:3:25", "return errors.New", "help:", "preview configured fixes:", "go tool repolint --fix --diff --fix-rules=structured-error --allow-structural"} {
			if !strings.Contains(output, expected) {
				t.Fatalf("missing %q:\n%s", expected, output)
			}
		}
		if stdout.Len() != 0 {
			t.Fatal("diagnostics leaked to stdout")
		}
	}
	t.Setenv("NO_COLOR", "")
	if useColor(&bytes.Buffer{}, "auto") || !useColor(&bytes.Buffer{}, "always") {
		t.Fatal("color override does not respect the selected mode")
	}
}
