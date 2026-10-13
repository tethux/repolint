package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type bindingOperation struct {
	Module         string `yaml:"module"`
	Operation      string `yaml:"operation"`
	Classification string `yaml:"classification"`
	CName          string `yaml:"c_name"`
	CppName        string `yaml:"cpp_name"`
	GoName         string `yaml:"go_name"`
	CSignature     string `yaml:"c_signature"`
	CppSignature   string `yaml:"cpp_signature"`
	GoSignature    string `yaml:"go_signature"`
	Line           int    `yaml:"-"`
}

type bindingSchema struct {
	SchemaVersion int `yaml:"schema_version"`
	Scope         struct {
		Required   []string `yaml:"required"`
		Deferred   []string `yaml:"deferred"`
		LegacyOnly []string `yaml:"legacy_only"`
	} `yaml:"scope"`
	Operations []bindingOperation `yaml:"operations"`
}

func schemaInputs(base string, patterns []string) ([]string, error) {
	var result []string
	for _, pattern := range patterns {
		matches, globErr := filepath.Glob(filepath.Join(base, pattern))
		if globErr != nil {
			return nil, failure("glob schema inputs", globErr)
		}
		if len(matches) == 0 {
			return nil, failure("schema input matched no files: "+pattern, nil)
		}
		for _, file := range matches {
			if !pathWithinRoot(base, file) {
				return nil, failure("schema input escapes repository", nil)
			}
			info, statErr := os.Lstat(file)
			if statErr != nil {
				return nil, failure("stat schema input", statErr)
			}
			if !info.Mode().IsRegular() {
				return nil, failure("schema input must be a regular file: "+file, nil)
			}
			if !slices.Contains(result, file) {
				result = append(result, file)
			}
		}
	}
	slices.Sort(result)
	return result, nil
}

func readSchema(base, file string) (bindingSchema, error) {
	data, readErr := readRepositoryFile(base, file)
	if readErr != nil {
		return bindingSchema{}, failure("read binding schema", readErr)
	}
	var schema bindingSchema
	if decodeErr := yaml.Unmarshal(data, &schema); decodeErr != nil {
		return schema, failure("decode binding schema", decodeErr)
	}
	if schema.SchemaVersion != 1 {
		return schema, failure("binding schema_version must be 1", nil)
	}
	var document yaml.Node
	if decodeErr := yaml.Unmarshal(data, &document); decodeErr != nil {
		return schema, failure("locate schema operations", decodeErr)
	}
	if len(document.Content) != 1 {
		return schema, failure("binding schema must contain one document", nil)
	}
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "operations" {
			for n, node := range root.Content[index+1].Content {
				if n < len(schema.Operations) {
					schema.Operations[n].Line = node.Line
				}
			}
		}
	}
	return schema, nil
}

func lintSchemaDrift(root string, opts options, report reporter, rule RuleConfig) error {
	absolute, resolveErr := filepath.Abs(root)
	if resolveErr != nil {
		return failure("resolve schema scope", resolveErr)
	}
	if absolute != opts.base {
		return nil
	}
	schemas, inputErr := schemaInputs(opts.base, rule.SchemaFiles)
	if inputErr != nil {
		return inputErr
	}
	selected := schemas[:0]
	for _, file := range schemas {
		relative, relErr := filepath.Rel(opts.base, file)
		if relErr != nil {
			return failure("resolve schema rule scope", relErr)
		}
		if rule.applies(filepath.ToSlash(relative)) {
			selected = append(selected, file)
		}
	}
	schemas = selected
	if len(schemas) == 0 {
		return nil
	}
	headers, headerErr := nativeSignatures(opts.base, rule.Headers, false)
	if headerErr != nil {
		return headerErr
	}
	definitions, definitionErr := nativeSignatures(opts.base, rule.CDefinitions, true)
	if definitionErr != nil {
		return definitionErr
	}
	goSymbols, goErr := goSignatures(opts.base, rule.GoFiles)
	if goErr != nil {
		return goErr
	}
	cppSources, cppErr := schemaSources(opts.base, rule.CppFiles)
	if cppErr != nil {
		return cppErr
	}
	allowed := map[string]bool{}
	for _, name := range rule.AllowCSymbols {
		allowed[name] = true
	}
	for _, file := range schemas {
		schema, schemaErr := readSchema(opts.base, file)
		if schemaErr != nil {
			return schemaErr
		}
		seen := map[string]bool{}
		for _, operation := range schema.Operations {
			position := token.Position{Filename: file, Line: operation.Line, Column: 1}
			emit := func(message string) {
				report(diagnostic{Rule: ruleSchemaDrift, Position: position, Message: message, Suggestion: "update the contract and all required binding layers together"})
			}
			if operation.Operation == "" || seen[operation.Operation] {
				emit("missing or duplicate operation name")
				continue
			}
			seen[operation.Operation] = true
			var scope []string
			switch operation.Classification {
			case "required":
				scope = schema.Scope.Required
			case "deferred":
				scope = schema.Scope.Deferred
			case "legacy_only":
				scope = schema.Scope.LegacyOnly
			default:
				emit("invalid operation classification: " + operation.Operation)
				continue
			}
			if !slices.Contains(scope, operation.Operation) {
				emit("operation classification disagrees with scope: " + operation.Operation)
			}
			if operation.CName != "" && operation.Classification == "required" {
				allowed[operation.CName] = true
			}
			if operation.Classification != "required" && operation.CName != "" && !allowed[operation.CName] {
				if _, exported := headers[operation.CName]; exported {
					emit("nonrequired C operation is exported without an explicit exception: " + operation.CName)
				}
			}
			if operation.Classification != "required" {
				continue
			}
			if operation.CName == "" || operation.CppName == "" || operation.GoName == "" {
				emit("required operation is missing a binding symbol: " + operation.Operation)
				continue
			}
			cSignature, declared := headers[operation.CName]
			implementation, implemented := definitions[operation.CName]
			if !declared || !implemented {
				emit("missing C declaration or definition: " + operation.CName)
			} else if cSignature != implementation {
				emit("C declaration and definition signatures disagree: " + operation.CName)
			}
			cppSignature, cppFound := cppSignature(cppSources, operation.CppName)
			if !cppFound {
				emit("missing public C++ member: " + operation.CppName)
			}
			goSignature, goFound := goSymbols[operation.GoName]
			if !goFound {
				emit("missing Go binding: " + operation.GoName)
			}
			for _, contract := range []struct {
				layer, want, got string
				exists           bool
			}{{"C", operation.CSignature, cSignature, declared}, {"C++", operation.CppSignature, cppSignature, cppFound}, {"Go", operation.GoSignature, goSignature, goFound}} {
				if rule.RequireSignatures && contract.want == "" {
					emit("missing " + contract.layer + " signature contract: " + operation.Operation)
				} else if contract.exists && contract.want != "" && compactNative(contract.want) != compactNative(contract.got) {
					emit(contract.layer + " signature drift: " + operation.Operation + "; expected " + contract.want + ", found " + contract.got)
				}
			}
		}
		for _, required := range schema.Scope.Required {
			if !seen[required] {
				report(diagnostic{Rule: ruleSchemaDrift, Position: token.Position{Filename: file, Line: 1, Column: 1}, Message: "required operation has no schema entry: " + required})
			}
		}
	}
	for symbol := range headers {
		if !allowed[symbol] {
			report(diagnostic{Rule: ruleSchemaDrift, Position: token.Position{Filename: schemas[0], Line: 1, Column: 1}, Message: "public C symbol is absent from schema: " + symbol})
		}
	}
	for source, target := range rule.HeaderCopies {
		original, readErr := readRepositoryFile(opts.base, filepath.Join(opts.base, source))
		if readErr != nil {
			return failure("read canonical header", readErr)
		}
		copyData, copyErr := readRepositoryFile(opts.base, filepath.Join(opts.base, target))
		if copyErr != nil {
			return failure("read copied header", copyErr)
		}
		if !bytes.Equal(original, copyData) {
			report(diagnostic{Rule: ruleSchemaDrift, Position: token.Position{Filename: filepath.Join(opts.base, target), Line: 1, Column: 1}, Message: "copied header differs from " + source, Suggestion: "regenerate the binding header from its canonical source"})
		}
	}
	if len(rule.Calls) > 0 {
		sources, readErr := schemaSources(opts.base, rule.CDefinitions)
		if readErr != nil {
			return readErr
		}
		callPattern := regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
		for file, text := range sources {
			for _, match := range callPattern.FindAllStringSubmatchIndex(text, -1) {
				name := text[match[2]:match[3]]
				if matchesCall(name, rule.Calls) {
					report(diagnostic{Rule: ruleSchemaDrift, Position: token.Position{Filename: file, Line: strings.Count(text[:match[0]], "\n") + 1, Column: 1}, Message: "forbidden native dependency: " + name, Suggestion: "use the typed domain bridge instead of a legacy command handler"})
				}
			}
		}
	}
	return nil
}

func schemaSources(base string, patterns []string) (map[string]string, error) {
	files, inputErr := schemaInputs(base, patterns)
	if inputErr != nil {
		return nil, inputErr
	}
	result := map[string]string{}
	for _, file := range files {
		data, readErr := readRepositoryFile(base, file)
		if readErr != nil {
			return nil, failure("read schema surface", readErr)
		}
		result[file] = stripNativeComments(string(data))
	}
	return result, nil
}

func goSignatures(base string, patterns []string) (map[string]string, error) {
	files, inputErr := schemaInputs(base, patterns)
	if inputErr != nil {
		return nil, inputErr
	}
	result := map[string]string{}
	for _, path := range files {
		data, readErr := readRepositoryFile(base, path)
		if readErr != nil {
			return nil, readErr
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, data, 0)
		if parseErr != nil {
			return nil, failure("parse Go binding surface", parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := file.Name.Name + "."
			if fn.Recv != nil {
				receiver := fn.Recv.List[0].Type
				if pointer, isPointer := receiver.(*ast.StarExpr); isPointer {
					receiver = pointer.X
				}
				ident, isIdent := receiver.(*ast.Ident)
				if !isIdent {
					continue
				}
				name += ident.Name + "."
			}
			name += fn.Name.Name
			fields := func(list *ast.FieldList) string {
				var values []string
				if list != nil {
					for _, field := range list.List {
						count := max(1, len(field.Names))
						for range count {
							values = append(values, renderExpr(fset, field.Type))
						}
					}
				}
				return "(" + strings.Join(values, ",") + ")"
			}
			result[name] = fields(fn.Type.Params) + fields(fn.Type.Results)
		}
	}
	return result, nil
}

var nativeWords = regexp.MustCompile(`[A-Za-z_]\w*|::|\S`)

func stripNativeComments(text string) string {
	masked := []byte(text)
	blank := func(start, end int) {
		for i := start; i < end; i++ {
			if masked[i] != '\n' {
				masked[i] = ' '
			}
		}
	}
	for i := 0; i < len(text); {
		start := i
		switch {
		case strings.HasPrefix(text[i:], "//"):
			for i < len(text) && text[i] != '\n' {
				i++
			}
			blank(start, i)
		case strings.HasPrefix(text[i:], "/*"):
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				i = len(text)
			} else {
				i += end + 4
			}
			blank(start, i)
		case strings.HasPrefix(text[i:], `R"`):
			open := strings.IndexByte(text[i+2:], '(')
			if open < 0 || open > 16 {
				i++
				continue
			}
			delimiter := text[i+2 : i+2+open]
			closing := ")" + delimiter + `"`
			end := strings.Index(text[i+3+open:], closing)
			if end < 0 {
				i = len(text)
			} else {
				i += 3 + open + end + len(closing)
			}
			blank(start, i)
		case text[i] == '"' || text[i] == '\'':
			quote := text[i]
			i++
			for i < len(text) {
				if text[i] == '\\' {
					i = min(i+2, len(text))
					continue
				}
				if text[i] == quote {
					i++
					break
				}
				i++
			}
			blank(start, i)
		default:
			i++
		}
	}
	return string(masked)
}
func compactNative(text string) string { return strings.Join(strings.Fields(text), "") }

func parameterTypes(text string) string {
	var parts []string
	start, depth := 0, 0
	for index, char := range text {
		switch char {
		case '<', '(', '{':
			depth++
		case '>', ')', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, text[start:index])
				start = index + 1
			}
		}
	}
	parts = append(parts, text[start:])
	var types []string
	for _, part := range parts {
		part, _, _ = strings.Cut(part, "=")
		tokens := nativeWords.FindAllString(part, -1)
		if len(tokens) == 0 || (len(tokens) == 1 && tokens[0] == "void") {
			continue
		}
		end := len(tokens) - 1
		for index, value := range tokens {
			if value == "[" {
				end = index - 1
				break
			}
		}
		if end > 0 && validIdentifier(tokens[end]) && tokens[end-1] != "::" {
			tokens = append(tokens[:end], tokens[end+1:]...)
		}
		types = append(types, strings.Join(tokens, ""))
	}
	return "(" + strings.Join(types, ",") + ")"
}

func nativeSignatures(base string, patterns []string, definitions bool) (map[string]string, error) {
	sources, sourceErr := schemaSources(base, patterns)
	if sourceErr != nil {
		return nil, sourceErr
	}
	pattern := regexp.MustCompile(`(?m)^\s*((?:const\s+)?[A-Za-z_]\w*(?:\s*\*+\s*|\s+))([A-Za-z_]\w*)\s*\(([^()]*)\)\s*;`)
	if definitions {
		pattern = regexp.MustCompile(`\bcfn\s+([A-Za-z_]\w*)\s*\(([^()]*)\)\s*->\s*((?:const\s+)?[A-Za-z_]\w*(?:\s*\*)?)\s*\{`)
	}
	result := map[string]string{}
	for _, text := range sources {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			name, signature := match[2], compactNative(match[1])+parameterTypes(match[3])
			if definitions {
				name, signature = match[1], compactNative(match[3])+parameterTypes(match[2])
			}
			if previous, found := result[name]; found && previous != signature {
				return nil, failure("conflicting native declarations: "+name, nil)
			}
			result[name] = signature
		}
		if definitions {
			traditional := regexp.MustCompile(`(?m)^\s*((?:const\s+)?[A-Za-z_]\w*(?:\s*\*+\s*|\s+))([A-Za-z_]\w*)\s*\(([^()]*)\)\s*\{`)
			for _, match := range traditional.FindAllStringSubmatch(text, -1) {
				name, signature := match[2], compactNative(match[1])+parameterTypes(match[3])
				if previous, found := result[name]; found && previous != signature {
					return nil, failure("conflicting native definitions: "+name, nil)
				}
				result[name] = signature
			}
		}
	}
	return result, nil
}

func cppSignature(sources map[string]string, symbol string) (string, bool) {
	parts := strings.Split(symbol, "::")
	if len(parts) != 3 {
		return "", false
	}
	classPattern := regexp.MustCompile(`\bclass\s+` + regexp.QuoteMeta(parts[1]) + `\b[^;{]*\{`)
	memberPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(strings.TrimPrefix(parts[2], "~")) + `\s*\(([^()]*)\)([^;{]*)[;{]`)
	if strings.HasPrefix(parts[2], "~") {
		memberPattern = regexp.MustCompile(`~` + regexp.QuoteMeta(parts[1]) + `\s*\(([^()]*)\)([^;{]*)[;{]`)
	}
	for _, text := range sources {
		if !regexp.MustCompile(`\bnamespace\s+` + regexp.QuoteMeta(parts[0]) + `\b`).MatchString(text) {
			continue
		}
		class := classPattern.FindStringIndex(text)
		if class == nil {
			continue
		}
		start, end, depth := class[1], class[1], 1
		for end < len(text) && depth > 0 {
			switch text[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		body := text[start : end-1]
		for _, indices := range memberPattern.FindAllStringSubmatchIndex(body, -1) {
			prefix := body[:indices[0]]
			if strings.LastIndex(prefix, "public:") < strings.LastIndex(prefix, "private:") || strings.LastIndex(prefix, "public:") < 0 {
				continue
			}
			prefix = prefix[strings.LastIndexAny(prefix, ";{}")+1:]
			if access := strings.LastIndex(prefix, "public:"); access >= 0 {
				prefix = prefix[access+len("public:"):]
			}
			prefix = regexp.MustCompile(`\[\[[^]]*\]\]|\bstatic\b|\binline\b`).ReplaceAllString(prefix, "")
			if strings.HasPrefix(parts[2], "~") {
				prefix = ""
			}
			return compactNative(prefix) + parameterTypes(body[indices[2]:indices[3]]) + compactNative(body[indices[4]:indices[5]]), true
		}
	}
	return "", false
}

func readRepositoryFile(base, file string) ([]byte, error) {
	relative, relErr := filepath.Rel(base, file)
	if relErr != nil {
		return nil, failure("resolve schema source", relErr)
	}
	root, openErr := os.OpenRoot(base)
	if openErr != nil {
		return nil, failure("open schema root", openErr)
	}
	data, readErr := root.ReadFile(relative)
	closeErr := root.Close()
	if readErr != nil || closeErr != nil {
		return nil, failure("read confined schema source", errors.Join(readErr, closeErr))
	}
	return data, nil
}
