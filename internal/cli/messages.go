package cli

// User-facing messages that are matched verbatim against the reference
// TypeScript CLI by the parity matrix. They are intentionally capitalized and
// punctuated — contrary to Go's error-string convention — because their exact
// text is a compatibility contract, not a Go-internal detail. The ones that
// were previously written out at more than one call site live here so the
// copies cannot drift apart. Do not reword without updating the parity matrix.
const (
	// msgLegacyFeature is returned when a bare-id (legacy) Feature is requested.
	msgLegacyFeature = "Legacy feature '%s' not supported. Please check https://containers.dev/features for replacements.\nIf you were hoping to use local Features, remember to prepend your Feature name with \"./\". Please check https://containers.dev/implementors/features-distribution/#addendum-locally-referenced for more information."

	// legacyFeaturePrefix is the stable prefix of msgLegacyFeature, used to
	// recognize that message as user-facing when classifying build errors.
	legacyFeaturePrefix = "Legacy feature '"

	// msgDockerBuildFailed is returned when `docker build` exits non-zero.
	msgDockerBuildFailed = "Command failed: docker build (exit %d): %s"
)
