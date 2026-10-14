package metadata

var (
	appName    string = "seiro"
	appDesc    string = "Virtual machine environments declared in Starlark, powered by cloud-hypervisor or QEMU and passt"
	authorName string = "Takumi Takahashi"
)

func AppName() string {
	return appName
}

func AppDesc() string {
	return appDesc
}

func AuthorName() string {
	return authorName
}
