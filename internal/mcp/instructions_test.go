package mcp

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gerwim/recarto/internal/features"
)

// toolNamed matches any Recarto MCP tool name as a whole word.
var toolNamed = func() *regexp.Regexp {
	var names []string
	for _, ns := range groups {
		for _, n := range ns {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\b`)
}()

// assertNamesRecartoTools fails unless every tool text names is preceded, on
// its line, by "recarto MCP tool", and text names at least one tool.
func assertNamesRecartoTools(t *testing.T, what, text string) {
	t.Helper()
	named := false
	for _, line := range strings.Split(text, "\n") {
		for _, m := range toolNamed.FindAllStringIndex(line, -1) {
			named = true
			if !strings.Contains(line[:m[0]], "recarto MCP tool") {
				t.Errorf("%s: %q is not called a recarto MCP tool:\n%s", what, line[m[0]:m[1]], line)
			}
		}
	}
	if !named {
		t.Errorf("%s names no tool:\n%s", what, text)
	}
}

func TestServerInstructionsNameOnlyRegisteredTools(t *testing.T) {
	a := newTestApp(t, "main")
	for _, tc := range []struct {
		name string
		f    features.Set
	}{
		{"all on", nil},
		{"memory-tools off", features.Set{features.MemoryTools: false}},
		{"graph-tools off", features.Set{features.GraphTools: false}},
		{"todos off", features.Set{features.Todos: false}},
		{"issue-reports off", features.Set{features.IssueReports: false}},
		{"every tool off", features.Set{features.MemoryTools: false, features.GraphTools: false, features.IssueReports: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.Features = tc.f
			cs := connect(t, a)
			instr := cs.InitializeResult().Instructions
			if len(instr) > 1000 {
				t.Errorf("instructions are %d characters, want at most 1000", len(instr))
			}
			tools := listTools(t, a)
			named := toolNamed.FindAllStringIndex(instr, -1)
			for _, m := range named {
				if n := instr[m[0]:m[1]]; tools[n] == nil {
					t.Errorf("names %s, which is not registered:\n%s", n, instr)
				}
			}
			if len(named) > 0 && !strings.Contains(instr[:named[0][0]], "`recarto` MCP server") {
				t.Errorf("names a tool before saying it is a tool of the recarto MCP server:\n%s", instr)
			}
			if tc.f == nil {
				for _, n := range []string{"recall_memory", "record_memory", "search_graph", "get_code_snippet"} {
					if !strings.Contains(instr, n) {
						t.Errorf("all on: %s not named:\n%s", n, instr)
					}
				}
			}
			if tc.name == "every tool off" && (len(named) > 0 || strings.Contains(instr, "MCP server")) {
				t.Errorf("with every tool off the text still points at tools:\n%s", instr)
			}
		})
	}
}

func TestResultTextsNameTheRecartoServer(t *testing.T) {
	for what, text := range map[string]string{
		"weakResultHint":  weakResultHint,
		"typeTraceHint":   fmt.Sprintf(typeTraceHint, "Store", "class", "Store", "Store", "Store"),
		"noCallersHint":   fmt.Sprintf(noCallersHint, "pkg.Run", "Run"),
		"testCallersHint": fmt.Sprintf(testCallersHint, "Run", "Run"),
		"feedbackKindErr": fmt.Sprintf(feedbackKindErr, "feedback"),
		"conflictNote":    conflictNote,
	} {
		assertNamesRecartoTools(t, what, text)
	}
}
