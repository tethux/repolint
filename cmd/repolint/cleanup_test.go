package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCleanupPolicies(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.no-discarded-cleanup-error]
enabled=true
calls=["*.Release", "os.Remove"]
[rules.cleanup-context-policy]
enabled=true
calls=["*.Release"]
functions=["rollback*"]
context_factories=["example.test/cleanup.NewContext"]
`)
	writeFixture(t, root, "source.go", `package p
import (
 "context"
 clean "example.test/cleanup"
 fs "os"
)
func ordinary(ctx context.Context) error {
 _ = m.Release(ctx, resources)
 m.Release(ctx, resources)
 defer m.Release(ctx, resources)
 go m.Release(ctx, resources)
 _, _ = m.ReleaseAsync(ctx, resources)
 _ = fs.Remove("temporary")
 _, handled = m.Release(ctx, resources), other()
 var _ = m.Release(ctx, resources)
 _ = m.Other(ctx)
 //repolint:ignore no-discarded-cleanup-error -- audited legacy boundary
 _ = m.Release(ctx, resources)
 return m.Release(ctx, resources)
}
func rollbackGood(ctx context.Context) error {
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 return m.Release(cleanupCtx, resources)
}
func rollbackCanceled(ctx context.Context) error {
 return m.Release(ctx, resources)
}
func rollbackUnbounded(ctx context.Context) error {
 return m.Release(context.WithoutCancel(ctx), resources)
}
func rollbackReassigned(ctx context.Context) error {
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 cleanupCtx = ctx
 return m.Release(cleanupCtx, resources)
}
func rollbackShadowed(ctx context.Context) error {
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 { cleanupCtx := ctx; if err := m.Release(cleanupCtx, resources); err != nil { return err } }
 return m.Release(cleanupCtx, resources)
}
func rollbackForwarded(ctx context.Context) error {
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 alias := cleanupCtx
 return m.Release(alias, resources)
}
func rollbackFactoryShadowed(ctx context.Context) error {
 clean := otherFactory
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 return m.Release(cleanupCtx, resources)
}
func rollbackEscape(ctx context.Context) error {
 cleanupCtx, cancel := clean.NewContext(ctx)
 defer cancel()
 mutate(&cleanupCtx)
 return m.Release(cleanupCtx, resources)
}
func rollbackSuppressed(ctx context.Context) error {
 //repolint:ignore cleanup-context-policy -- live context supplied by test harness
 return m.Release(ctx, resources)
}
`)
	counts := map[string]int{}
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { counts[d.Rule]++ }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if counts[ruleDiscardedCleanup] != 7 || counts[ruleCleanupContext] != 7 {
		t.Fatalf("diagnostics: %v", counts)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--quiet", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cleanup error is discarded") || !strings.Contains(stderr.String(), "approved factory") {
		t.Fatal(stderr.String())
	}
}

func TestCleanupConfigValidation(t *testing.T) {
	for _, config := range []string{
		`[rules.no-discarded-cleanup-error]
enabled=true
calls=[]`,
		`[rules.no-discarded-cleanup-error]
fix="safe"`,
		`[rules.no-discarded-cleanup-error]
calls=["["]`,
		`[rules.cleanup-context-policy]
enabled=true`,
		`[rules.cleanup-context-policy]
context_arg=-1`,
		`[rules.cleanup-context-policy]
context_factories=["helper"]`,
	} {
		t.Run(config, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, ".repolint.toml", "schema=1\n"+config)
			if _, _, configErr := loadConfig(root, ""); configErr == nil {
				t.Fatal("invalid cleanup policy accepted")
			}
		})
	}
}
