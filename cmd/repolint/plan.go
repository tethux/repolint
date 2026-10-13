package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type fileEdit struct {
	Path          string
	Before, After []byte
	Mode          fs.FileMode
	Exists        bool
}

func walkSource(root string, opts options, visit func(string, []byte) error) error {
	abs, resolveErr := filepath.Abs(root)
	if resolveErr != nil {
		return failure("resolve source root", resolveErr)
	}
	repository, openErr := os.OpenRoot(opts.base)
	if openErr != nil {
		return failure("open source root", openErr)
	}
	walkErr := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, entryErr error) error {
		if entryErr != nil {
			return failure("walk source", entryErr)
		}
		rel, relErr := filepath.Rel(opts.base, path)
		if relErr != nil || !pathWithinRoot(opts.base, path) {
			return failure("source outside configuration root", relErr)
		}
		if opts.cfg.excluded(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		src, readErr := repository.ReadFile(rel)
		if readErr != nil {
			return failure("read "+path, readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, src, parser.ParseComments)
		if parseErr != nil {
			return failure("parse "+path, parseErr)
		}
		if opts.cfg.Files.ExcludeGenerated && ast.IsGenerated(file) {
			return nil
		}
		return visit(path, src)
	})
	closeErr := repository.Close()
	if walkErr != nil {
		return walkErr
	}
	if closeErr != nil {
		return failure("close source root", closeErr)
	}
	return nil
}

func (opts options) permits(name string, structural bool) bool {
	rule := opts.cfg.Rules[name]
	if !rule.Enabled || rule.Fix == "off" {
		return false
	}
	if opts.fixRules != "" && !slices.Contains(strings.Split(opts.fixRules, ","), name) {
		return false
	}
	return !structural || (opts.allowStructural && rule.Fix == "structural")
}

// planFixes runs the existing AST fixers in an isolated tree before any writes.
func planFixes(root string, opts options, report reporter) error {
	stage, stageErr := os.MkdirTemp("", "repolint-plan-")
	if stageErr != nil {
		return failure("stage fixes", stageErr)
	}
	resultErr := planInStage(root, stage, opts, report)
	cleanupErr := os.RemoveAll(stage)
	if resultErr != nil {
		return resultErr
	}
	if cleanupErr != nil {
		return failure("clean fix stage", cleanupErr)
	}
	return nil
}

func planInStage(root, stage string, opts options, report reporter) error {
	originals := map[string]fileEdit{}
	copyErr := walkSource(opts.base, opts, func(path string, src []byte) error {
		rel, relErr := filepath.Rel(opts.base, path)
		if relErr != nil {
			return failure("stage source path", relErr)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return failure("stat source", statErr)
		}
		originals[rel] = fileEdit{Path: rel, Before: src, Mode: info.Mode().Perm(), Exists: true}
		target := filepath.Join(stage, rel)
		if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o700); mkdirErr != nil {
			return failure("stage directory", mkdirErr)
		}
		if writeErr := os.WriteFile(target, src, 0o600); writeErr != nil {
			return failure("stage source", writeErr)
		}
		return nil
	})
	if copyErr != nil {
		return copyErr
	}
	moduleData, moduleErr := os.ReadFile(filepath.Join(opts.base, "go.mod"))
	if moduleErr == nil {
		if writeErr := os.WriteFile(filepath.Join(stage, "go.mod"), moduleData, 0o600); writeErr != nil {
			return failure("stage module", writeErr)
		}
	} else if !errors.Is(moduleErr, os.ErrNotExist) {
		return failure("read module", moduleErr)
	}
	absoluteRoot, rootErr := filepath.Abs(root)
	if rootErr != nil {
		return failure("resolve fix root", rootErr)
	}
	relativeRoot, relErr := filepath.Rel(opts.base, absoluteRoot)
	if relErr != nil {
		return failure("resolve fix scope", relErr)
	}
	stageRoot := filepath.Join(stage, relativeRoot)
	if _, scopeErr := os.Stat(stageRoot); errors.Is(scopeErr, os.ErrNotExist) {
		return nil
	} else if scopeErr != nil {
		return failure("stat fix scope", scopeErr)
	}
	stagedOpts := opts
	stagedOpts.base = stage
	if opts.permits(ruleStructuredError, false) {
		if fixErr := fixStructuredErrors(stageRoot, stagedOpts); fixErr != nil {
			return fixErr
		}
	}
	if opts.permits(ruleExpensiveAssertion, false) {
		fixErr := walkSource(stageRoot, stagedOpts, func(path string, src []byte) error {
			rel, pathErr := filepath.Rel(stage, path)
			if pathErr != nil {
				return failure("assertion fix path", pathErr)
			}
			rule := opts.cfg.Rules[ruleExpensiveAssertion]
			if !rule.applies(filepath.ToSlash(rel)) {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, src, parser.ParseComments)
			if parseErr != nil {
				return failure("parse assertion fix", parseErr)
			}
			if !guardResolvable(file, rule.Guard) {
				return nil
			}
			if !fixAssertions(file, fset, rule) {
				return nil
			}
			var out bytes.Buffer
			if formatErr := format.Node(&out, fset, file); formatErr != nil {
				return failure("format assertion fix", formatErr)
			}
			if writeErr := os.WriteFile(path, out.Bytes(), 0o600); writeErr != nil {
				return failure("stage assertion fix", writeErr)
			}
			return nil
		})
		if fixErr != nil {
			return fixErr
		}
	}
	var edits []fileEdit
	collectOpts := stagedOpts
	collectOpts.cfg.Files.ExcludeDirs = nil
	collectOpts.cfg.Files.ExcludeGenerated = false
	collectErr := walkSource(stage, collectOpts, func(path string, src []byte) error {
		rel, pathErr := filepath.Rel(stage, path)
		if pathErr != nil {
			return failure("collect fix path", pathErr)
		}
		if opts.cfg.excluded(rel) {
			return failure("fix would write an excluded file: "+rel, nil)
		}
		edit := originals[rel]
		if bytes.Equal(edit.Before, src) {
			return nil
		}
		if !pathWithinRoot(stageRoot, path) {
			return failure("fix would write outside selected root: "+rel, nil)
		}
		edit.Path = rel
		edit.After = src
		if !edit.Exists {
			edit.Mode = 0o644
			// Excluded or generated existing files are absent from the staged tree.
			_, statErr := os.Lstat(filepath.Join(opts.base, rel))
			if !errors.Is(statErr, os.ErrNotExist) {
				return failure("refuse replacing excluded or existing file: "+rel, statErr)
			}
		}
		edits = append(edits, edit)
		return nil
	})
	if collectErr != nil {
		return collectErr
	}
	slices.SortFunc(edits, func(a, b fileEdit) int { return strings.Compare(a.Path, b.Path) })
	if rule := opts.cfg.Rules[ruleDiscardedCleanup]; rule.Enabled && rule.TypeCheck {
		overlay := map[string][]byte{}
		for _, edit := range edits {
			overlay[filepath.Join(opts.base, edit.Path)] = edit.After
		}
		index, typeErr := loadTypeIndex(root, opts, rule, overlay)
		if typeErr != nil {
			return typeErr
		}
		opts.types = index
	}
	if opts.diff {
		for _, edit := range edits {
			if diffErr := printDiffTo(opts.output, edit); diffErr != nil {
				return diffErr
			}
		}
	} else {
		if applyErr := applyPlan(opts.base, edits); applyErr != nil {
			return applyErr
		}
	}
	if opts.stats != nil {
		opts.stats.Changed += len(edits)
	}
	// Report diagnostics against the planned result, using original source paths.
	return walkSource(stageRoot, stagedOpts, func(path string, src []byte) error {
		rel, pathErr := filepath.Rel(stage, path)
		if pathErr != nil {
			return failure("resolve diagnostic path", pathErr)
		}
		return lintSource(filepath.Join(opts.base, rel), src, opts, report)
	})
}

func guardResolvable(file *ast.File, guard string) bool {
	parts := strings.Split(guard, ".")
	if len(parts) == 2 {
		if _, exists := importAliases(file)[parts[0]]; !exists {
			return false
		}
		shadowed := false
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == parts[0] && ident.Obj != nil {
				shadowed = true
			}
			return !shadowed
		})
		return !shadowed
	}
	return file.Scope != nil && file.Scope.Lookup(guard) != nil
}

func applyPlan(base string, edits []fileEdit) error {
	repository, openErr := os.OpenRoot(base)
	if openErr != nil {
		return failure("open fix root", openErr)
	}
	applyErr := applyEdits(repository, edits)
	closeErr := repository.Close()
	if applyErr != nil {
		return applyErr
	}
	if closeErr != nil {
		return failure("close fix root", closeErr)
	}
	return nil
}

func applyEdits(repository *os.Root, edits []fileEdit) error {
	missingDirs, dirErr := missingDirectories(repository, edits)
	if dirErr != nil {
		return dirErr
	}
	for _, edit := range edits {
		current, readErr := repository.ReadFile(edit.Path)
		if edit.Exists {
			if readErr != nil || !bytes.Equal(current, edit.Before) {
				return failure("source changed while planning: "+edit.Path, readErr)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return failure("new fix file already exists: "+edit.Path, readErr)
		}
	}
	for index, edit := range edits {
		writeErr := writeEdit(repository, edit.Path, edit.After, edit.Mode)
		if writeErr != nil {
			var rollbackErr error
			for _, previous := range edits[:index] {
				var restoreErr error
				if previous.Exists {
					restoreErr = writeEdit(repository, previous.Path, previous.Before, previous.Mode)
				} else {
					restoreErr = repository.Remove(previous.Path)
				}
				rollbackErr = errors.Join(rollbackErr, restoreErr)
			}
			for _, dir := range missingDirs {
				if _, statErr := repository.Stat(dir); errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				rollbackErr = errors.Join(rollbackErr, repository.Remove(dir))
			}
			return failure("apply fix "+edit.Path, errors.Join(writeErr, rollbackErr))
		}
	}
	return nil
}

func writeEdit(repository *os.Root, path string, data []byte, mode fs.FileMode) error {
	if mkdirErr := repository.MkdirAll(filepath.Dir(path), 0o750); mkdirErr != nil {
		return failure("create fix directory", mkdirErr)
	}
	temp, openErr := repository.OpenFile(path+".repolint-tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if openErr != nil {
		return failure("create temporary fix", openErr)
	}
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		removeErr := repository.Remove(path + ".repolint-tmp")
		return failure("write temporary fix", errors.Join(writeErr, closeErr, removeErr))
	}
	if renameErr := repository.Rename(path+".repolint-tmp", path); renameErr != nil {
		removeErr := repository.Remove(path + ".repolint-tmp")
		return failure("replace source", errors.Join(renameErr, removeErr))
	}
	return nil
}

func missingDirectories(repository *os.Root, edits []fileEdit) ([]string, error) {
	missing := map[string]bool{}
	for _, edit := range edits {
		for dir := filepath.Dir(edit.Path); dir != "."; dir = filepath.Dir(dir) {
			info, statErr := repository.Stat(dir)
			if statErr == nil {
				if !info.IsDir() {
					return nil, failure("fix parent is not a directory: "+dir, nil)
				}
				break
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				return nil, failure("stat fix directory", statErr)
			}
			missing[dir] = true
		}
	}
	dirs := make([]string, 0, len(missing))
	for dir := range missing {
		dirs = append(dirs, dir)
	}
	slices.SortFunc(dirs, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return dirs, nil
}
