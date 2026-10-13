package main

import "testing"

func TestNativeLengthBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".repolint.toml", `schema=1
[rules.native-length]
enabled=true
sources=["*.size","written","C.count"]
exclude_functions=["checkedSize"]
`)
	writeFixture(t, root, "source.go", `package p
import "C"
func bad(){ _=int(value.size);_=buffer[:written];_=make([]byte,value.size);_=int(C.count()) }
func good(){ size,err:=checkedSize(value.size);if err!=nil{return};_=buffer[:size];_=int(ordinary) }
func checkedSize(n int) int { return int(value.size) }
`)
	var diagnostics []diagnostic
	if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
		t.Fatal(lintErr)
	}
	if len(diagnostics) != 4 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
