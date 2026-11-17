package mcp

import (
	"strings"

	"github.com/gerwim/recarto/internal/features"
)

// serverInstructions is the usage text the server returns in its initialize
// reply for the features in f. It names only tools newServer registers under f.
func serverInstructions(f features.Set) string {
	var b strings.Builder
	b.WriteString("Recarto keeps memory that persists across sessions (preferences, learnings, decisions, do-not-repeats, bugs)")
	if f.On(features.GraphTools) {
		b.WriteString(" and a code graph of the repo")
	}
	b.WriteString(".")
	if f.On(features.MemoryTools) || f.On(features.GraphTools) || f.On(features.IssueReports) {
		b.WriteString(" Its tools are those of the `recarto` MCP server: if your host loads tools on demand, load them by that name before calling them.")
	}
	if f.On(features.MemoryTools) {
		b.WriteString(" Before changing code, call `recall_memory` with the task and `search_bugs`. Record what you learn with `record_memory` and bug fixes with `record_bug`, anchored to the files or symbols they concern, and record a `preference` when the user corrects your approach.")
		if f.On(features.Todos) {
			b.WriteString(" Track open work as a `todo` memory and close it with `complete_todo`.")
		}
	}
	if f.On(features.GraphTools) {
		b.WriteString(" Instead of reading whole files or grepping, use `search_graph` to find symbols, `get_code_snippet` or `get_file_outline` to read them, `search_content` for text, and `find_references` or `trace_call_path` for uses and calls.")
	}
	if f.On(features.IssueReports) {
		b.WriteString(" Report problems with Recarto itself with `report_recarto_issue`.")
	}
	return b.String()
}
