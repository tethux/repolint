package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

type sourceLine struct {
	Text      string
	NoNewline bool
}
type diffLine struct {
	Kind byte
	Line sourceLine
}

func sourceLines(src []byte) []sourceLine {
	if len(src) == 0 {
		return nil
	}
	text := string(src)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	result := make([]sourceLine, len(lines))
	for index, line := range lines {
		result[index] = sourceLine{Text: line, NoNewline: index == len(lines)-1 && !strings.HasSuffix(text, "\n")}
	}
	return result
}

func lineChanges(old, next []sourceLine) []diffLine {
	prefix := 0
	for prefix < min(len(old), len(next)) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < min(len(old)-prefix, len(next)-prefix) && old[len(old)-suffix-1] == next[len(next)-suffix-1] {
		suffix++
	}
	var result []diffLine
	appendLine := func(kind byte, line sourceLine) { result = append(result, diffLine{Kind: kind, Line: line}) }
	for _, line := range old[:prefix] {
		appendLine(' ', line)
	}
	left, right := old[prefix:len(old)-suffix], next[prefix:len(next)-suffix]
	// Bound the table for large rewrites; the fallback remains a valid patch.
	if len(left) > 0 && len(right) > 1000000/len(left) {
		for _, line := range left {
			appendLine('-', line)
		}
		for _, line := range right {
			appendLine('+', line)
		}
	} else {
		width := len(right) + 1
		table := make([]int, (len(left)+1)*width)
		for i := len(left) - 1; i >= 0; i-- {
			for j := len(right) - 1; j >= 0; j-- {
				if left[i] == right[j] {
					table[i*width+j] = table[(i+1)*width+j+1] + 1
				} else {
					table[i*width+j] = max(table[(i+1)*width+j], table[i*width+j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(left) || j < len(right) {
			switch {
			case i < len(left) && j < len(right) && left[i] == right[j]:
				appendLine(' ', left[i])
				i++
				j++
			case j < len(right) && (i == len(left) || table[i*width+j+1] > table[(i+1)*width+j]):
				appendLine('+', right[j])
				j++
			default:
				appendLine('-', left[i])
				i++
			}
		}
	}
	for _, line := range old[len(old)-suffix:] {
		appendLine(' ', line)
	}
	return result
}

func unifiedDiff(edit fileEdit) string {
	lines := lineChanges(sourceLines(edit.Before), sourceLines(edit.After))
	oldName := "a/" + strings.ReplaceAll(edit.Path, "\\", "/")
	if !edit.Exists {
		oldName = "/dev/null"
	}
	var out strings.Builder
	out.WriteString("--- " + oldName + "\n+++ b/" + strings.ReplaceAll(edit.Path, "\\", "/") + "\n")
	oldPos, newPos := 1, 1
	for index := 0; index < len(lines); {
		if lines[index].Kind == ' ' {
			oldPos++
			newPos++
			index++
			continue
		}
		start := max(0, index-3)
		oldStart, newStart := oldPos-(index-start), newPos-(index-start)
		end := index + 1
		lastChange := index
		for end < len(lines) {
			if lines[end].Kind != ' ' {
				lastChange = end
			} else if end-lastChange > 6 {
				break
			}
			end++
		}
		end = min(len(lines), lastChange+4)
		oldCount, newCount := 0, 0
		for _, line := range lines[start:end] {
			if line.Kind != '+' {
				oldCount++
			}
			if line.Kind != '-' {
				newCount++
			}
		}
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		out.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))
		for _, line := range lines[start:end] {
			out.WriteByte(line.Kind)
			out.WriteString(line.Line.Text)
			out.WriteByte('\n')
			if line.Line.NoNewline {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
		for _, line := range lines[index:end] {
			if line.Kind != '+' {
				oldPos++
			}
			if line.Kind != '-' {
				newPos++
			}
		}
		index = end
	}
	return out.String()
}

func printDiffTo(output io.Writer, edit fileEdit) error {
	if output == nil {
		output = os.Stdout
	}
	if _, writeErr := io.WriteString(output, unifiedDiff(edit)); writeErr != nil {
		return failure("write diff", writeErr)
	}
	return nil
}
