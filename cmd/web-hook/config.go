package main

import (
	"fmt"
	"strings"

	envstruct "code.cloudfoundry.org/go-envstruct"
	"code.cloudfoundry.org/log-cache/internal/uaa"
)

// Config is the configuration for a LogCache Gateway.
type Config struct {
	LogCacheAddr string `env:"LOG_CACHE_ADDR, required"`
	HealthPort   int    `env:"HEALTH_PORT"`
	UAA          uaa.UAA

	GroupPrefix string `env:"GROUP_PREFIX"`

	// Encoded as SourceID=TemplatePath
	TemplatePaths []templateInfo `env:"TEMPLATE_PATHS"`

	// Encoded as SourceID=TemplatePath
	FollowTemplatePaths []templateInfo `env:"FOLLOW_TEMPLATE_PATHS"`
}

// LoadConfig creates Config object from environment variables
func LoadConfig() (*Config, error) {
	c := Config{
		HealthPort: 6063,
	}

	if err := envstruct.Load(&c); err != nil {
		return nil, err
	}

	return &c, nil
}

type templateInfo struct {
	SourceIDs    []string
	TemplatePath string
}

// UnmarshalEnv implements envstruct.Unmarshaller. It expects the data to be
// of the form: SourceID=TemplatePath
func (i *templateInfo) UnmarshalEnv(s string) error {
	if s == "" {
		return nil
	}

	r := strings.Split(s, "=")
	if len(r) != 2 {
		return fmt.Errorf("%s is not of valid form. (SourceID-1;SourceID-2=TemplatePath)", s)
	}

	sourceIDs := r[0]

	i.SourceIDs = strings.Split(sourceIDs, ";")
	i.TemplatePath = r[1]
	return nil
}
