package docker

import "strings"

func BuildEntrypointScript(customEntrypoints []string) string {
	return "echo Container started\ntrap \"exit 0\" 15\n" + strings.Join(customEntrypoints, "\n") + "\nexec \"$@\"\nwhile sleep 1 & wait $!; do :; done"
}
