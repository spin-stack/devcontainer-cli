package product

var (
	version = "dev"
	name    = "devcontainer"
)

type Info struct {
	Name    string
	Version string
}

func Get() Info {
	return Info{Name: name, Version: version}
}
