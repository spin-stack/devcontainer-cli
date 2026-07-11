package product

var (
	version = "dev"
	name    = "devcontainer"
)

type Config struct {
	Name    string
	Version string
}

func GetConfig() Config {
	return Config{Name: name, Version: version}
}
