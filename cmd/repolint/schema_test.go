package main

import (
	"strings"
	"testing"
)

func TestSchemaDrift(t *testing.T) {
	config := `schema=1
[rules.schema-drift]
enabled=true
schema_files=["contract.yaml"]
headers=["api.h"]
c_definitions=["api.cpp"]
cpp_files=["module.cppm"]
go_files=["api.go"]
header_copies={"api.h"="copy.h"}
`
	schema := `schema_version: 1
scope: {required: [create]}
operations:
 - module: handle
   operation: create
   classification: required
   c_name: api_create
   cpp_name: example::handle::create
   go_name: p.New
   c_signature: result(constchar*,handle**)
   cpp_signature: result<handle>(std::string_view)noexcept
   go_signature: (string)(*Handle,error)
`
	files := map[string]string{
		"contract.yaml": schema,
		"api.h":         "result api_create(const char *name, handle **output);\n",
		"copy.h":        "result api_create(const char *name, handle **output);\n",
		"api.cpp":       "cfn api_create(const char *other, handle **out) -> result { return success; }",
		"module.cppm":   "export namespace example { class handle { public: static result<handle> create(std::string_view name) noexcept; }; }",
		"api.go":        "package p; type Handle struct{}; func New(name string) (*Handle,error) {return nil,nil}",
	}
	for _, mutation := range []struct{ name, file, old, new, diagnostic string }{
		{name: "valid"},
		{name: "C definition mismatch", file: "api.cpp", old: "const char *other", new: "int other", diagnostic: "signatures disagree"},
		{name: "C signature drift", file: "contract.yaml", old: "constchar*", new: "int", diagnostic: "C signature drift"},
		{name: "missing Go method", file: "api.go", old: "func New", new: "func Renamed", diagnostic: "missing Go binding"},
		{name: "Go signature drift", file: "api.go", old: "name string", new: "name int", diagnostic: "Go signature drift"},
		{name: "C++ signature drift", file: "module.cppm", old: "std::string_view name", new: "int name", diagnostic: "C++ signature drift"},
		{name: "private C++ method", file: "module.cppm", old: "public:", new: "private:", diagnostic: "missing public C++ member"},
		{name: "header copy drift", file: "copy.h", old: "name", new: "renamed", diagnostic: "copied header differs"},
		{name: "missing scope entry", file: "contract.yaml", old: "required: [create]", new: "required: [create, another]", diagnostic: "required operation has no schema entry"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, ".repolint.toml", config)
			for file, data := range files {
				if file == mutation.file {
					data = strings.Replace(data, mutation.old, mutation.new, 1)
				}
				writeFixture(t, root, file, data)
			}
			var diagnostics []diagnostic
			if lintErr := lintRoot(root, options{}, func(d diagnostic) { diagnostics = append(diagnostics, d) }); lintErr != nil {
				t.Fatal(lintErr)
			}
			if mutation.diagnostic == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("diagnostics: %+v", diagnostics)
				}
				return
			}
			found := false
			for _, d := range diagnostics {
				if d.Rule == ruleSchemaDrift && strings.Contains(d.Message, mutation.diagnostic) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %q: %+v", mutation.diagnostic, diagnostics)
			}
		})
	}
}

func TestNativeLexerMasksCommentsAndStrings(t *testing.T) {
	text := stripNativeComments("// result ghost();\nconst char *text=\"result ghost();\";\nconst char *raw=R\"tag(result ghost();)tag\";\nresult actual(void);")
	if strings.Contains(text, "ghost") || !strings.Contains(text, "actual") {
		t.Fatal(text)
	}
}
