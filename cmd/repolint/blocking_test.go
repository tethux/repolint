package main

import "testing"

func TestBlockingUnderLock(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.blocking-under-lock]
enabled=true
guard="mu"
assume_locked_functions=["broadcastLocked"]
calls=["*.Wait"]
`)
	writeFixture(t, root, "source.go", `package p
func send(){mu.Lock();defer mu.Unlock();channel<-value}
func receive(){mu.RLock();defer mu.RUnlock();value:=<-channel;use(value)}
func wait(){mu.Lock();defer mu.Unlock();worker.Wait()}
func broadcastLocked(){channel<-value}
func selectBlocked(){mu.Lock();select{case channel<-value:}}
func selectSafe(){mu.Lock();select{case channel<-value:default:};mu.Unlock()}
func released(){mu.Lock();mu.Unlock();channel<-value}
func goroutine(){mu.Lock();go func(){channel<-value}()}
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 5 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
