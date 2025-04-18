// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package servicediscovery

import (
	"github.com/DataDog/datadog-agent/pkg/telemetry"
)

var (
	metricDiscoveredServices = telemetry.NewGaugeWithOpts(
		CheckName,
		"discovered_services",
		[]string{},
		"Number of discovered alive services.",
		telemetry.Options{NoDoubleUnderscoreSep: true},
	)
)
