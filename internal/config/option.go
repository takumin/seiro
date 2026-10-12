package config

type Option interface {
	Apply(*Config)
}

type LogLevel string

func (o LogLevel) Apply(c *Config) {
	c.LogLevel = string(o)
}

type LogFormat string

func (o LogFormat) Apply(c *Config) {
	c.LogFormat = string(o)
}

type File string

func (o File) Apply(c *Config) {
	c.File = string(o)
}
