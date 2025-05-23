// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build containerd && trivy

// Package containerd holds containerd related files
package containerd

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"

	"github.com/DataDog/datadog-agent/comp/core/config"
	workloadmeta "github.com/DataDog/datadog-agent/comp/core/workloadmeta/def"
	"github.com/DataDog/datadog-agent/pkg/sbom"
	"github.com/DataDog/datadog-agent/pkg/sbom/collectors"
	cutil "github.com/DataDog/datadog-agent/pkg/util/containerd"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/util/option"
	"github.com/DataDog/datadog-agent/pkg/util/trivy"
)

// resultChanSize defines the result channel size
// 1000 is already a very large default value
const resultChanSize = 1000

// scanRequest defines a scan request. This struct should be
// hashable to be pushed in the work queue for processing.
type scanRequest struct {
	imageID string
}

type scannerFunc func(ctx context.Context, imgMeta *workloadmeta.ContainerImageMetadata, img containerd.Image, client cutil.ContainerdItf, scanOptions sbom.ScanOptions) (sbom.Report, error)

// NewScanRequest creates a new scan request
func NewScanRequest(imageID string) sbom.ScanRequest {
	return scanRequest{imageID: imageID}
}

// Collector returns the collector name
func (r scanRequest) Collector() string {
	return collectors.ContainerdCollector
}

// Type returns the scan request type
func (r scanRequest) Type(opts sbom.ScanOptions) string {
	if opts.UseMount {
		return sbom.ScanFilesystemType
	}
	return sbom.ScanDaemonType
}

// ID returns the scan request ID
func (r scanRequest) ID() string {
	return r.imageID
}

// Collector defines a containerd collector
type Collector struct {
	trivyCollector   *trivy.Collector
	resChan          chan sbom.ScanResult
	opts             sbom.ScanOptions
	containerdClient cutil.ContainerdItf
	wmeta            option.Option[workloadmeta.Component]

	closed bool
}

// CleanCache cleans the cache
func (c *Collector) CleanCache() error {
	return c.trivyCollector.CleanCache()
}

// Init initializes the collector
func (c *Collector) Init(cfg config.Component, wmeta option.Option[workloadmeta.Component]) error {
	trivyCollector, err := trivy.GetGlobalCollector(cfg, wmeta)
	if err != nil {
		return err
	}
	c.wmeta = wmeta
	c.trivyCollector = trivyCollector
	c.opts = sbom.ScanOptionsFromConfigForContainers(cfg)
	return nil
}

// Scan performs the scan
func (c *Collector) Scan(ctx context.Context, request sbom.ScanRequest) sbom.ScanResult {
	imageID := request.ID()

	if c.containerdClient == nil {
		cl, err := cutil.NewContainerdUtil()
		if err != nil {
			return sbom.ScanResult{Error: fmt.Errorf("error creating containerd client: %s", err)}
		}
		c.containerdClient = cl
	}

	wmeta, ok := c.wmeta.Get()
	if !ok {
		return sbom.ScanResult{Error: fmt.Errorf("workloadmeta store is not initialized")}
	}
	imageMeta, err := wmeta.GetImage(imageID)
	if err != nil {
		return sbom.ScanResult{Error: fmt.Errorf("image metadata not found for image id %s: %s", imageID, err)}
	}
	log.Infof("containerd scan request [%v]: scanning image %v", imageID, imageMeta.Name)

	image, err := c.containerdClient.Image(imageMeta.Namespace, imageMeta.Name)
	if err != nil {
		return sbom.ScanResult{Error: fmt.Errorf("error getting image %s/%s: %s", imageMeta.Namespace, imageMeta.Name, err)}
	}

	var report sbom.Report
	var scanner scannerFunc
	if c.opts.UseMount {
		scanner = c.trivyCollector.ScanContainerdImageFromFilesystem
	} else if c.opts.OverlayFsScan {
		scanner = c.trivyCollector.ScanContainerdImageFromSnapshotter
	} else {
		scanner = c.trivyCollector.ScanContainerdImage
	}
	report, err = scanner(ctx, imageMeta, image, c.containerdClient, c.opts)
	scanResult := sbom.ScanResult{
		Error:   err,
		Report:  report,
		ImgMeta: imageMeta,
	}

	return scanResult
}

// Type returns the container image scan type
func (c *Collector) Type() collectors.ScanType {
	return collectors.ContainerImageScanType
}

// Channel returns the channel to send scan results
func (c *Collector) Channel() chan sbom.ScanResult {
	return c.resChan
}

// Options returns the collector options
func (c *Collector) Options() sbom.ScanOptions {
	return c.opts
}

// Shutdown shuts down the collector
func (c *Collector) Shutdown() {
	if c.resChan != nil && !c.closed {
		close(c.resChan)
	}
	c.closed = true
}

func init() {
	collectors.RegisterCollector(collectors.ContainerdCollector, &Collector{
		resChan: make(chan sbom.ScanResult, resultChanSize),
	})
}
