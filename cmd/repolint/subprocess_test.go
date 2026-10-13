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
func alias(ctx context.Context){stepCtx:=ctx; if limited {stepCtx,cancel:=context.WithTimeout(stepCtx,second);defer cancel();shell.CommandContext(stepCtx,"tool")};shell.CommandContext(stepCtx,"tool")}
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

func TestTypedContextFactoryIdentity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.test/factories\ngo 1.26.4\n")
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.subprocess-context]
enabled=true
type_check=true
context_factories=["example.test/factories.Command.Context"]
`)
	writeFixture(t, root, "source.go", `package p
import "context"
import "os/exec"
type Command struct{}
func (Command) Context() context.Context {return context.Background()}
type Wrong struct{}
func (Wrong) Context() context.Context {return context.Background()}
func good(command Command){ctx:=command.Context();exec.CommandContext(ctx,"tool")}
func bad(command Wrong){ctx:=command.Context();exec.CommandContext(ctx,"tool")}
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 1 || diagnostics[0].Rule != ruleSubprocessContext {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
