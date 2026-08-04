package buildinfo

// Version is overridden at build time with -ldflags when a release artifact is produced.
var Version = "interim/master.xxxx"

// Commit and BuildDate are populated for release binaries through linker flags.
var (
	Commit    = "unknown"
	BuildDate = "unknown"
)
