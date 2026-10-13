package main

import "testing"

func TestSubprocessContexts(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.subprocess-context]
enabled=true
`)
	writeFixture(t, root, "source.go", `package p
import "context"
import shell "os/exec"
func good(ctx context.Context){shell.CommandContext(ctx,"tool")}
func timeout(){ctx,cancel:=context.WithTimeout(context.Background(),second);defer cancel();shell.CommandContext(ctx,"tool")}
func background(){shell.CommandContext(context.Background(),"tool")}
func detached(ctx context.Context){shell.CommandContext(context.WithoutCancel(ctx),"tool")}
func reassigned(ctx context.Context){ctx=context.Background();shell.CommandContext(ctx,"tool")}
func plain(){shell.Command("tool")}
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 4 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
