package templates

import (
	_ "embed"
)

//go:embed test-node.sh.tmpl
var scriptTemplate string

func ScriptTemplate() string {
	return scriptTemplate
}
