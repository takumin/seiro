package version

var (
	version  string = "unknown"
	revision string = "unknown"
)

func Version() string {
	return version
}

func Revision() string {
	return revision
}
