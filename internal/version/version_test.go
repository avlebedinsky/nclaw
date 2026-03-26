package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func saveVars() (string, string, string, string, string) {
	return Version, Commit, BuildDate, BuildNumber, DockerTag
}

func restoreVars(v, c, bd, bn, dt string) {
	Version = v
	Commit = c
	BuildDate = bd
	BuildNumber = bn
	DockerTag = dt
}

func TestString_Defaults(t *testing.T) {
	v, c, bd, bn, dt := saveVars()
	defer restoreVars(v, c, bd, bn, dt)

	Version = "dev"
	Commit = "unknown"
	BuildDate = "unknown"
	BuildNumber = ""
	DockerTag = ""

	result := String()
	assert.Equal(t, "version=dev commit=unknown built=unknown", result)
}

func TestString_WithBuildNumber(t *testing.T) {
	v, c, bd, bn, dt := saveVars()
	defer restoreVars(v, c, bd, bn, dt)

	Version = "1.0.0"
	Commit = "abc123"
	BuildDate = "2026-01-01"
	BuildNumber = "42"
	DockerTag = ""

	result := String()
	assert.Equal(t, "version=1.0.0 commit=abc123 built=2026-01-01 build=#42", result)
}

func TestString_WithDockerTag(t *testing.T) {
	v, c, bd, bn, dt := saveVars()
	defer restoreVars(v, c, bd, bn, dt)

	Version = "1.0.0"
	Commit = "abc123"
	BuildDate = "2026-01-01"
	BuildNumber = ""
	DockerTag = "latest"

	result := String()
	assert.Equal(t, "version=1.0.0 commit=abc123 built=2026-01-01 docker=latest", result)
}

func TestString_WithBothBuildNumberAndDockerTag(t *testing.T) {
	v, c, bd, bn, dt := saveVars()
	defer restoreVars(v, c, bd, bn, dt)

	Version = "2.0.0"
	Commit = "def456"
	BuildDate = "2026-03-26"
	BuildNumber = "99"
	DockerTag = "multi-model"

	result := String()
	assert.Equal(t, "version=2.0.0 commit=def456 built=2026-03-26 build=#99 docker=multi-model", result)
}
