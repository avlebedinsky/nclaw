//go:build !unix

package procrun

import "os/exec"

func configure(*exec.Cmd) {}

func sweep(*exec.Cmd) {}
