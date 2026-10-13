package main

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

type sourceLocation struct {
	File         string
	Line, Column int
}
type typeIndex struct {
	files   map[string]bool
	calls   map[sourceLocation]*types.Signature
	symbols map[sourceLocation]string
}

func location(pos token.Position) sourceLocation {
	return sourceLocation{filepath.Clean(pos.Filename), pos.Line, pos.Column}
}

func loadTypeIndex(root string, opts options, overlays ...map[string][]byte) (*typeIndex, error) {
	directories := map[string]bool{}
	includeTests := false
	collectErr := walkSource(root, opts, func(file string, _ []byte) error {
		rel, relErr := filepath.Rel(opts.base, file)
		if relErr != nil {
			return failure("resolve typed source", relErr)
		}
		for _, rule := range opts.cfg.Rules {
			if rule.TypeCheck && rule.applies(filepath.ToSlash(rel)) {
				directories[filepath.Dir(file)] = true
				includeTests = includeTests || rule.IncludeTests
			}
		}
		return nil
	})
	if collectErr != nil {
		return nil, collectErr
	}
	index := &typeIndex{files: map[string]bool{}, calls: map[sourceLocation]*types.Signature{}, symbols: map[sourceLocation]string{}}
	if len(directories) == 0 {
		return index, nil
	}
	patterns := make([]string, 0, len(directories))
	for directory := range directories {
		patterns = append(patterns, directory)
	}
	slices.Sort(patterns)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	config := &packages.Config{Context: ctx, Dir: opts.base, Mode: packages.LoadSyntax, Tests: includeTests}
	if len(overlays) != 0 {
		config.Overlay = overlays[0]
	}
	if len(opts.cfg.Analysis.BuildTags) != 0 {
		config.BuildFlags = []string{"-tags=" + strings.Join(opts.cfg.Analysis.BuildTags, ",")}
	}
	loaded, loadErr := packages.Load(config, patterns...)
	if loadErr != nil {
		return nil, failure("load Go types", loadErr)
	}
	var problems []string
	for _, pkg := range loaded {
		for _, issue := range pkg.Errors {
			problems = append(problems, issue.Error())
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.GoFiles {
			index.files[filepath.Clean(file)] = true
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if signature, isSignature := pkg.TypesInfo.TypeOf(call.Fun).(*types.Signature); isSignature {
					key := location(pkg.Fset.Position(call.Pos()))
					index.calls[key] = signature
					var object types.Object
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						object = pkg.TypesInfo.Uses[fun]
					case *ast.SelectorExpr:
						object = pkg.TypesInfo.Uses[fun.Sel]
					}
					if function, yes := object.(*types.Func); yes && function.Pkg() != nil {
						symbol := function.Pkg().Path() + "."
						methodSignature, hasSignature := function.Type().(*types.Signature)
						if hasSignature && methodSignature.Recv() != nil {
							receiver := methodSignature.Recv().Type()
							if pointer, ok := receiver.(*types.Pointer); ok {
								receiver = pointer.Elem()
							}
							if named, ok := receiver.(*types.Named); ok {
								symbol += named.Obj().Name() + "."
							}
						}
						index.symbols[key] = symbol + function.Name()
					}
				}
				return true
			})
		}
	}
	if len(problems) != 0 {
		return nil, failure("type checking failed: "+strings.Join(problems, "\n"), nil)
	}
	return index, nil
}

func lintTypedCleanup(fset *token.FileSet, file *ast.File, report reporter, rule RuleConfig, index *typeIndex) error {
	if !rule.TypeCheck {
		lintDiscardedCleanup(fset, file, report, rule)
		return nil
	}
	if index == nil {
		return failure("typed cleanup requires package analysis; run without --fix", nil)
	}
	if !index.files[filepath.Clean(fset.Position(file.Pos()).Filename)] {
		return nil
	}
	var missing bool
	check := func(d diagnostic) {
		signature, found := index.calls[location(d.Position)]
		if !found {
			missing = true
			return
		}
		results := signature.Results()
		if results.Len() == 0 {
			return
		}
		errorType, ok := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
		if ok && types.Implements(results.At(results.Len()-1).Type(), errorType) {
			report(d)
		}
	}
	lintDiscardedCleanup(fset, file, check, rule)
	if missing {
		return failure("type information missing for cleanup call in "+fset.Position(file.Pos()).Filename, nil)
	}
	return nil
}

func requiresTypes(cfg Config) bool {
	for _, rule := range cfg.Rules {
		if rule.Enabled && rule.TypeCheck {
			return true
		}
	}
	return false
}

func resolvedCallName(fset *token.FileSet, call *ast.CallExpr, imports map[string]string, indexes ...*typeIndex) string {
	if len(indexes) > 0 && indexes[0] != nil {
		if symbol, found := indexes[0].symbols[location(fset.Position(call.Pos()))]; found {
			return symbol
		}
	}
	return cleanupCallName(call, imports)
}
