// Package templates provides the built-in script template and template functions.
package templates

import (
	_ "embed"
	"strings"
	"text/template"
)

//go:embed test-node.sh.tmpl
var scriptTemplate string

// ScriptTemplate returns the built-in script template.
func ScriptTemplate() string {
	return scriptTemplate
}

// FuncMap returns functions available in script templates.
//
//   - shquote: quotes a string for POSIX shells with single quotes
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"shquote": ShellQuote,
	}
}

// ShellQuote quotes s with single quotes so that it is interpreted literally by POSIX shells.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
