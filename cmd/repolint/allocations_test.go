package main

import "testing"

func TestNativeAllocationOwnership(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.native-allocation]
enabled=true
`)
	writeFixture(t, root, "source.go", `package p
import "C"
import "unsafe"
func good(){ p:=C.CString("name");defer C.free(unsafe.Pointer(p)); use(p) }
func leak(){ p:=C.CString("name");use(p) }
func late(){ p:=C.CString("name");if fail{return};defer C.free(unsafe.Pointer(p)) }
func wrong(){ p:=C.CString("name");defer C.free(unsafe.Pointer(other));use(p) }
func hidden(){ use(C.CString("name")) }
func shadow(){ p:=C.CString("name");{p:=other;defer C.free(unsafe.Pointer(p))} }
func wrapped(){ p:=C.CString("name");defer C.free(unsafe.Pointer(steal(p))) }
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 6 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
