package templates

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellQuote(t *testing.T) {
	cases := []string{
		"",
		"simple",
		"with space",
		"it's",
		`"double" $HOME ${x} $(echo no) ` + "`echo no`",
		"^(TestA|TestB)$",
		"new\nline",
	}
	for _, s := range cases {
		quoted := ShellQuote(s)
		out, err := exec.Command("bash", "-c", "printf '%s' "+quoted).Output()
		require.NoError(t, err, quoted)
		assert.Equal(t, s, string(out))
	}
}
