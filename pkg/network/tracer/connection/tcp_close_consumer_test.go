// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux_bpf

package connection

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTcpCloseConsumerStopRace(t *testing.T) {
	c := newTCPCloseConsumer(nil, nil)
	require.NotNil(t, c)

	c.Stop()
	c.FlushPending()
}
