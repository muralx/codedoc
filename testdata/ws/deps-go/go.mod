module github.com/fixture/depsgo

go 1.27

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/acme/absent v0.1.0
	github.com/acme/kit v1.4.0
	github.com/fixture/lib v0.0.0
)

require github.com/acme/indirect v1.0.0 // indirect

replace github.com/fixture/lib => ../go-lib
