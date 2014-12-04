package version

type Version struct {
	GitCommit string
	Version string
}

var VersionInfo = Version{
	GitCommit: gitCommit,
	Version: version,
}

const gitCommit = "unknown"
const version = "0.1"
