package source_test

import (
	"regexp"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/source"
	"github.com/stretchr/testify/require"
)

func TestMatchesIgnoreNonCode(t *testing.T) {
	pattern := regexp.MustCompile(`call\("([^"]+)"\)`)
	for _, text := range []string{
		`// call("wrong")` + "\n" + `call("/healthz")`,
		`/* outer /* call("wrong") */ comment */ call("/healthz")`,
		`"call(\"wrong\")"; call("/healthz")`,
		`r#"call("wrong")"#; call("/healthz")`,
		`"""call("wrong")"""; call("/healthz")`,
		`@"call(""wrong"")"; call("/healthz")`,
		`char c = '"'; call("/healthz")`,
	} {
		parsed := source.Read(text)
		matches := parsed.Matches(pattern)
		require.Len(t, matches, 1, text)
		require.Equal(t, "/healthz", matches[0][1])
		require.Len(t, parsed.Code, len(text))
		require.Len(t, parsed.Literal, len(text))
	}
}
