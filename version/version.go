package version

type Version struct {
	GitSHA string
	Version string
}

var VersionInfo = Version{
	GitSHA: gitSha,
	Version: version,
}

const gitSha = "unknown"
const version = "0.1"
