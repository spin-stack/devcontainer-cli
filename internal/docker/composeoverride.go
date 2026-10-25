package docker

import "strings"

// BuildEntrypointScript generates the /bin/sh -c script that chains feature
// entrypoints (e.g., docker-init.sh) with a sleep loop, as TS singleContainer.ts
// spawnDevContainer writes it: the entrypoints' line is there even when there
// are none, as an empty line.
func BuildEntrypointScript(customEntrypoints []string) string {
	return "echo Container started\ntrap \"exit 0\" 15\n" + strings.Join(customEntrypoints, "\n") + "\nexec \"$@\"\nwhile sleep 1 & wait $!; do :; done"
}
