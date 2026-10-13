package main

import "testing"

func TestNativeLockPaths(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.native-call-lock]
enabled=true
calls=["native.Call"]
guard="mu"
`)
	writeFixture(t, root, "source.go", `package p
import native "native"
func unguarded() { native.Call() }
func guarded() { mu.Lock(); defer mu.Unlock(); native.Call() }
func unlocked() { mu.Lock(); mu.Unlock(); native.Call() }
func conditional(flag bool) { if flag { mu.Lock() }; native.Call() }
func early(flag bool) { mu.Lock(); if flag { mu.Unlock();return };native.Call() }
func shadow() { mu.Lock(); { mu := other; mu.Unlock() }; native.Call() }
func asynchronous() { mu.Lock(); go native.Call() }
func scoped() { if flag { mu.Lock(); native.Call();mu.Unlock() } }
func wrongLock() { mu:=other;mu.Lock();native.Call() }
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 5 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
