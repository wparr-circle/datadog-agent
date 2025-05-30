// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !linux

package module

import (
	sysconfigtypes "github.com/DataDog/datadog-agent/pkg/system-probe/config/types"
)

// Factory encapsulates the initialization of a Module
type Factory struct {
	Name             sysconfigtypes.ModuleName
	ConfigNamespaces []string
	Fn               func(cfg *sysconfigtypes.Config, deps FactoryDependencies) (Module, error)
}
