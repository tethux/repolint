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
	files map[string]bool
	calls map[sourceLocation]*types.Signature
}

func location(pos token.Position) sourceLocation {
	return sourceLocation{filepath.Clean(pos.Filename), pos.Line, pos.Column}
}

func loadTypeIndex(root string, opts options, rule RuleConfig, overlays ...map[string][]byte) (*typeIndex, error) {
	directories := map[string]bool{}
	collectErr := walkSource(root, opts, func(file string, _ []byte) error {
		rel, relErr := filepath.Rel(opts.base, file)
		if relErr != nil {
			return failure("resolve typed source", relErr)
		}
		if rule.applies(filepath.ToSlash(rel)) {
			directories[filepath.Dir(file)] = true
		}
		return nil
	})
	if collectErr != nil {
		return nil, collectErr
	}
	index := &typeIndex{files: map[string]bool{}, calls: map[sourceLocation]*types.Signature{}}
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
	config := &packages.Config{Context: ctx, Dir: opts.base, Mode: packages.LoadSyntax, Tests: rule.IncludeTests}
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
					index.calls[location(pkg.Fset.Position(call.Pos()))] = signature
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
