package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type diagnosticRenderer struct{ color bool }

func useColor(output io.Writer, mode string) bool {
	if mode != "auto" {
		return mode == "always"
	}
	if _, exists := os.LookupEnv("NO_COLOR"); exists || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, statErr := file.Stat()
	return statErr == nil && info.Mode()&os.ModeCharDevice != 0
}

func (r diagnosticRenderer) paint(style, text string) string {
	if !r.color {
		return text
	}
	return "\x1b[" + style + "m" + text + "\x1b[0m"
}

func (r diagnosticRenderer) render(d diagnostic) string {
	var out strings.Builder
	out.WriteString(r.paint("1;31", "error["+d.Rule+"]") + ": " + r.paint("1", d.Message) + "\n")
	width := len(strconv.Itoa(d.Position.Line))
	gutter := strings.Repeat(" ", width+1)
	out.WriteString(gutter + r.paint("1;34", "-->") + " " + displayPosition(d.Position) + "\n")
	if d.SourceLine != "" {
		column := min(max(d.Position.Column-1, 0), len(d.SourceLine))
		end := column + 1
		if d.End.Line == d.Position.Line {
			end = d.End.Column - 1
		} else if d.End.Line > d.Position.Line {
			end = len(d.SourceLine)
		}
		end = min(max(end, column), len(d.SourceLine))
		prefix := expandedSource(d.SourceLine[:column], 0)
		span := expandedSource(d.SourceLine[column:end], len([]rune(prefix)))
		out.WriteString(gutter + r.paint("1;34", "|") + "\n")
		out.WriteString(r.paint("1;34", fmt.Sprintf("%*d |", width, d.Position.Line)) + " " + expandedSource(d.SourceLine, 0) + "\n")
		out.WriteString(gutter + r.paint("1;34", "|") + " " + strings.Repeat(" ", len([]rune(prefix))) + r.paint("1;31", strings.Repeat("^", max(1, len([]rune(span))))) + "\n")
	}
	if d.Suggestion != "" {
		out.WriteString(gutter + r.paint("1;36", "help:") + " " + d.Suggestion + "\n")
	}
	if d.FixCommand != "" {
		out.WriteString(gutter + r.paint("1;36", "hint:") + " preview configured fixes:\n" + gutter + "      " + r.paint("1;32", d.FixCommand) + "\n")
	}
	out.WriteByte('\n')
	return out.String()
}

func (r diagnosticRenderer) summary(text string, hasIssues bool) string {
	if hasIssues {
		return r.paint("1;31", text)
	}
	return r.paint("32", text)
}

func diagnosticSourceLine(src []byte, offset int) string {
	if offset < 0 || offset > len(src) {
		return ""
	}
	start := strings.LastIndexByte(string(src[:offset]), '\n') + 1
	line, _, _ := strings.Cut(string(src[start:]), "\n")
	return strings.TrimSuffix(line, "\r")
}

// Expand tabs consistently and keep terminal control characters out of excerpts.
func expandedSource(source string, column int) string {
	var out strings.Builder
	for _, char := range source {
		if char == '\t' {
			spaces := 4 - column%4
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		}
		if unicode.IsControl(char) {
			char = '�'
		}
		out.WriteRune(char)
		column++
	}
	return out.String()
}

func previewFixCommand(d diagnostic, opts options) string {
	if opts.fix || (d.Rule != ruleStructuredError && d.Rule != ruleExpensiveAssertion) {
		return ""
	}
	rule := opts.cfg.Rules[d.Rule]
	if rule.Fix == "off" {
		return ""
	}
	command := "go tool repolint --fix --diff --fix-rules=" + d.Rule
	if rule.Fix == "structural" {
		command += " --allow-structural"
	}
	if opts.configPath != "" {
		command += " --config " + shellArgument(opts.configPath)
	}
	root := opts.base
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if rel, relErr := filepath.Rel(cwd, root); relErr == nil {
			root = rel
		}
	}
	return command + " " + shellArgument(root)
}

func shellArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char) && !strings.ContainsRune("/._-", char)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
