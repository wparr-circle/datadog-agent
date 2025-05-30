// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux_bpf

package events

import (
	"math"
	"os"
	"slices"
	"sync"

	manager "github.com/DataDog/ebpf-manager"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/features"

	ddebpf "github.com/DataDog/datadog-agent/pkg/ebpf"
	"github.com/DataDog/datadog-agent/pkg/ebpf/names"
	"github.com/DataDog/datadog-agent/pkg/network/config"
	"github.com/DataDog/datadog-agent/pkg/network/usm/utils"
	"github.com/DataDog/datadog-agent/pkg/util/kernel"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

// Configure a given `*manager.Manager` for event processing
// This essentially instantiates the perf map/ring buffers and configure the
// eBPF maps where events are enqueued.
// Note this must be called *before* manager.InitWithOptions
func Configure(cfg *config.Config, proto string, m *manager.Manager, o *manager.Options) {
	if alreadySetUp(proto, m) {
		return
	}

	numCPUs, err := kernel.PossibleCPUs()
	if err != nil {
		numCPUs = 96
		log.Error("unable to detect number of CPUs. assuming 96 cores")
	}

	configureBatchMaps(proto, o, numCPUs)

	useRingBuffer := cfg.EnableUSMRingBuffers && features.HaveMapType(ebpf.RingBuf) == nil
	utils.AddBoolConst(o, useRingBuffer, "use_ring_buffer")

	bufferSize := cfg.USMKernelBufferPages * os.Getpagesize()

	if useRingBuffer {
		setupPerfRing(proto, m, o, numCPUs, cfg.USMDataChannelSize, bufferSize)
	} else {
		setupPerfMap(proto, m, cfg.USMDataChannelSize, bufferSize)
	}
}

func setupPerfMap(proto string, m *manager.Manager, dataChannelSize, perfEventBufferSize int) {
	handler := ddebpf.NewPerfHandler(dataChannelSize)
	mapName := eventMapName(proto)
	pm := &manager.PerfMap{
		Map: manager.Map{Name: mapName},
		PerfMapOptions: manager.PerfMapOptions{
			PerfRingBufferSize: perfEventBufferSize,

			// Our events are already batched on the kernel side, so it's
			// desirable to have Watermark set to 1
			Watermark: 1,

			RecordHandler: handler.RecordHandler,
			LostHandler:   handler.LostHandler,
			RecordGetter:  handler.RecordGetter,
		},
	}
	// The map appears as we list it in the Protocol struct.
	m.Maps = slices.DeleteFunc(m.Maps, func(currentMap *manager.Map) bool {
		return currentMap.Name == mapName
	})

	m.PerfMaps = append(m.PerfMaps, pm)
	removeRingBufferHelperCalls(m)
	setHandler(proto, handler)
}

func setupPerfRing(proto string, m *manager.Manager, o *manager.Options, numCPUs int, dataChannelSize, ringBufferSize int) {
	handler := ddebpf.NewRingBufferHandler(dataChannelSize)
	mapName := eventMapName(proto)
	// Adjusting ring buffer size with the number of CPUs and rounding it to the nearest power of 2
	ringBufferSize = toPowerOf2(numCPUs * ringBufferSize)
	rb := &manager.RingBuffer{
		Map: manager.Map{Name: mapName},
		RingBufferOptions: manager.RingBufferOptions{
			RecordHandler: handler.RecordHandler,
			RecordGetter:  handler.RecordGetter,
		},
	}

	// The map appears as we list it in the Protocol struct.
	m.Maps = slices.DeleteFunc(m.Maps, func(currentMap *manager.Map) bool {
		return currentMap.Name == mapName
	})

	o.MapSpecEditors[mapName] = manager.MapSpecEditor{
		Type:       ebpf.RingBuf,
		MaxEntries: uint32(ringBufferSize),
		KeySize:    0,
		ValueSize:  0,
		EditorFlag: manager.EditType | manager.EditMaxEntries | manager.EditKeyValue,
	}

	m.RingBuffers = append(m.RingBuffers, rb)
	setHandler(proto, handler)
}

func configureBatchMaps(proto string, o *manager.Options, numCPUs int) {
	if o.MapSpecEditors == nil {
		o.MapSpecEditors = make(map[string]manager.MapSpecEditor)
	}

	o.MapSpecEditors[proto+batchMapSuffix] = manager.MapSpecEditor{
		MaxEntries: uint32(numCPUs * batchPagesPerCPU),
		EditorFlag: manager.EditMaxEntries,
	}
}

func eventMapName(proto string) string {
	return proto + eventsMapSuffix
}

// removeRingBufferHelperCalls is called only in the context of kernels that
// don't support ring buffers. our eBPF code looks more or less like the
// following:
//
//	if (ring_buffers_supported) {
//	    bpf_ringbuf_output();
//	} else {
//	    bpf_perf_event_output();
//	}
//
// where `ring_buffers_supported` is an injected constant. The code above seems
// to work on the vast majority of kernel versions due to dead code elimination
// by the verifier, so for kernels that don't support ring buffers
// (ring_buffers_supported=0) we only see the perf event helper call when doing
// a program dump:
//
// bpf_perf_event_output();
//
// *However* in some instances this is not working on 4.14, so here we
// essentially replace `bpf_ringbuf_output` helper calls by a noop operation so
// they don't result in verifier errors even when deadcode elimination fails.
func removeRingBufferHelperCalls(m *manager.Manager) {
	// TODO: this is not the intended API usage of a `ebpf.Modifier`.
	// Once we have access to the `ddebpf.Manager`, add this modifier to its list of
	// `EnabledModifiers` and let it control the execution of the callbacks
	patcher := ddebpf.NewHelperCallRemover(asm.FnRingbufOutput, asm.FnRingbufQuery, asm.FnRingbufReserve, asm.FnRingbufSubmit, asm.FnRingbufDiscard)
	err := patcher.BeforeInit(m, names.NewModuleName("usm"), nil)

	if err != nil {
		// Our production code is actually loading on all Kernels we test on CI
		// (including those that don't support Ring Buffers) *even without
		// patching*, presumably due to pruning/dead code elimination. The only
		// thing failing to load was actually a small eBPF test program. So we
		// added the patching almost as an extra safety layer.
		//
		// All that to say that even if the patching fails, there's still a good
		// chance that the program will succeed to load. If it doesn't,there
		// isn't much we can do, and the loading error will bubble up and be
		// appropriately handled by the upstream code, which is why we don't do
		// anything here.
		log.Errorf("error patching eBPF bytecode: %s", err)
	}
}

func alreadySetUp(proto string, m *manager.Manager) bool {
	// check if we already have configured this perf map this can happen in the
	// context of a failed program load succeeded by another attempt
	mapName := eventMapName(proto)
	for _, perfMap := range m.PerfMaps {
		if perfMap.Map.Name == mapName {
			return true
		}
	}
	for _, perfMap := range m.RingBuffers {
		if perfMap.Map.Name == mapName {
			return true
		}
	}

	return false
}

// handlerByProtocol acts as registry holding a temporary reference to a
// `ddebpf.Handler` instance for a given protocol. This is done to simplify the
// usage of this package a little bit, so a call to `events.Configure` can be
// later linked to a call to `events.NewConsumer` without the need to explicitly
// propagate any values. The map is guarded by `handlerMux`.
var handlerByProtocol map[string]ddebpf.EventHandler
var handlerMux sync.Mutex

func getHandler(proto string) ddebpf.EventHandler {
	handlerMux.Lock()
	defer handlerMux.Unlock()
	if handlerByProtocol == nil {
		return nil
	}

	handler := handlerByProtocol[proto]
	delete(handlerByProtocol, proto)
	return handler
}

func setHandler(proto string, handler ddebpf.EventHandler) {
	handlerMux.Lock()
	defer handlerMux.Unlock()
	if handlerByProtocol == nil {
		handlerByProtocol = make(map[string]ddebpf.EventHandler)
	}
	handlerByProtocol[proto] = handler
}

// toPowerOf2 converts a number to its nearest power of 2
func toPowerOf2(x int) int {
	log := math.Log2(float64(x))
	return int(math.Pow(2, math.Round(log)))
}
