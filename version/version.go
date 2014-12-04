package version

type Version struct {
	GitCommit string
	Version   string
}

var VersionInfo = unknownVersion

var unknownVersion = Version{
	GitCommit: "unknown git commit",
	Version: "unknown version",
}
