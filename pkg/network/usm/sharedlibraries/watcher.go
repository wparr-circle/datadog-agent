// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux_bpf

package sharedlibraries

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/DataDog/datadog-agent/pkg/network/config"
	"github.com/DataDog/datadog-agent/pkg/network/protocols/telemetry"
	"github.com/DataDog/datadog-agent/pkg/network/usm/consts"
	"github.com/DataDog/datadog-agent/pkg/network/usm/utils"
	"github.com/DataDog/datadog-agent/pkg/process/monitor"
	"github.com/DataDog/datadog-agent/pkg/util/kernel"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

var (
	// The interval of the periodic scan for terminated processes. Increasing the interval, might cause larger spikes in cpu
	// and lowering it might cause constant cpu usage. This is a var instead of a const only because the test code changes
	// this value to speed up test execution.
	scanTerminatedProcessesInterval = 30 * time.Second
)

// ToLibPath casts the perf event data to the LibPath structure
func ToLibPath(data []byte) LibPath {
	return *(*LibPath)(unsafe.Pointer(&data[0]))
}

// ToBytes converts the libpath to a byte array containing the path
func ToBytes(l *LibPath) []byte {
	return l.Buf[:l.Len]
}

func (l *LibPath) String() string {
	return string(ToBytes(l))
}

// Rule is a rule to match against a shared library path
type Rule struct {
	Re           *regexp.Regexp
	RegisterCB   func(utils.FilePath) error
	UnregisterCB func(utils.FilePath) error
}

// Watcher provides a way to tie callback functions to the lifecycle of shared libraries
type Watcher struct {
	syncMutex      sync.RWMutex
	wg             sync.WaitGroup
	done           chan struct{}
	procRoot       string
	rules          []Rule
	processMonitor *monitor.ProcessMonitor
	registry       *utils.FileRegistry
	ebpfProgram    *EbpfProgram
	libset         Libset
	thisPID        int
	scannedPIDs    map[uint32]int

	// telemetry
	libHits    *telemetry.Counter
	libMatches *telemetry.Counter

	mapsCleaner func(map[uint32]struct{})
}

// Validate that Watcher implements the Attacher interface.
var _ utils.Attacher = &Watcher{}

// NewWatcher creates a new Watcher instance
func NewWatcher(cfg *config.Config, libset Libset, mapsCleaner func(map[uint32]struct{}), rules ...Rule) (*Watcher, error) {
	ebpfProgram := GetEBPFProgram(&cfg.Config)
	err := ebpfProgram.InitWithLibsets(libset)
	if err != nil {
		return nil, fmt.Errorf("error initializing shared library program: %w", err)
	}

	return &Watcher{
		wg:             sync.WaitGroup{},
		done:           make(chan struct{}),
		procRoot:       kernel.ProcFSRoot(),
		rules:          rules,
		libset:         libset,
		processMonitor: monitor.GetProcessMonitor(),
		ebpfProgram:    ebpfProgram,
		registry:       utils.NewFileRegistry(consts.USMModuleName, "shared_libraries"),
		scannedPIDs:    make(map[uint32]int),

		libHits:    telemetry.NewCounter("usm.so_watcher.hits", telemetry.OptPrometheus),
		libMatches: telemetry.NewCounter("usm.so_watcher.matches", telemetry.OptPrometheus),

		mapsCleaner: mapsCleaner,
	}, nil
}

// Stop the Watcher
func (w *Watcher) Stop() {
	if w == nil {
		return
	}

	close(w.done)
	w.wg.Wait()
}

type parseMapsFileCB func(path string)

// parseMapsFile takes in a bufio.Scanner representing a memory mapping of /proc/<PID>/maps file, and a callback to be
// applied on the paths extracted from the file. We're extracting only actual paths on the file system, and ignoring
// anonymous memory regions.
//
// Example for entries in the `maps` file:
// 7f135146b000-7f135147a000 r--p 00000000 fd:00 268743 /usr/lib/x86_64-linux-gnu/libm-2.31.so
// 7f135147a000-7f1351521000 r-xp 0000f000 fd:00 268743 /usr/lib/x86_64-linux-gnu/libm-2.31.so
// 7f1351521000-7f13515b8000 r--p 000b6000 fd:00 268743 /usr/lib/x86_64-linux-gnu/libm-2.31.so
// 7f13515b8000-7f13515b9000 r--p 0014c000 fd:00 268743 /usr/lib/x86_64-linux-gnu/libm-2.31.so
func parseMapsFile(scanner *bufio.Scanner, callback parseMapsFileCB) {
	// The maps file can have multiple entries of the same loaded file, the cache is meant to ensure, we're not wasting
	// time and memory on "duplicated" hooking.
	cache := make(map[string]struct{})
	for scanner.Scan() {
		line := scanner.Text()
		cols := strings.Fields(line)
		// ensuring we have exactly 6 elements (skip '(deleted)' entries) in the line, and the 4th element (inode) is
		// not zero (indicates it is a path, and not an anonymous path).
		if len(cols) == 6 && cols[4] != "0" {
			// Check if we've seen the same path before, if so, continue to the next.
			if _, exists := cache[cols[5]]; exists {
				continue
			}
			// We didn't process the path, so cache it to avoid future re-processing.
			cache[cols[5]] = struct{}{}

			// Apply the given callback on the path.
			callback(cols[5])
		}
	}
}

// DetachPID detaches a given pid from the eBPF program
func (w *Watcher) DetachPID(pid uint32) error {
	return w.registry.Unregister(pid)
}

// AttachPID attaches a given pid to the eBPF program
func (w *Watcher) AttachPID(pid uint32) error {
	mapsPath := fmt.Sprintf("%s/%d/maps", w.procRoot, pid)
	maps, err := os.Open(mapsPath)
	if err != nil {
		return err
	}
	defer maps.Close()

	registerErrors := make([]error, 0)
	successfulMatches := make([]string, 0)
	// Creating a callback to be applied on the paths extracted from the `maps` file.
	// We're creating the callback here, as we need the pid (which varies between iterations).
	parseMapsFileCallback := func(path string) {
		// Iterate over the rule, and look for a match.
		for _, r := range w.rules {
			if r.Re.MatchString(path) {
				if err := w.registry.Register(path, pid, r.RegisterCB, r.UnregisterCB, utils.IgnoreCB); err != nil {
					registerErrors = append(registerErrors, err)
				} else {
					successfulMatches = append(successfulMatches, path)
				}
				break
			}
		}
	}
	scanner := bufio.NewScanner(bufio.NewReader(maps))
	parseMapsFile(scanner, parseMapsFileCallback)

	if len(successfulMatches) == 0 {
		if len(registerErrors) == 0 {
			return fmt.Errorf("no rules matched for pid %d", pid)
		}
		return fmt.Errorf("no rules matched for pid %d, errors: %v", pid, registerErrors)
	}
	if len(registerErrors) > 0 {
		return fmt.Errorf("partially hooked (%v), errors while attaching pid %d: %v", successfulMatches, pid, registerErrors)
	}
	return nil
}

func (w *Watcher) handleLibraryOpen(lib LibPath) {
	if int(lib.Pid) == w.thisPID {
		// don't scan ourself
		return
	}

	w.libHits.Add(1)
	path := ToBytes(&lib)
	for _, r := range w.rules {
		if r.Re.Match(path) {
			w.libMatches.Add(1)
			_ = w.registry.Register(string(path), lib.Pid, r.RegisterCB, r.UnregisterCB, utils.IgnoreCB)
			break
		}
	}
}

// Start consuming shared-library events
func (w *Watcher) Start() {
	if w == nil {
		return
	}

	var err error
	w.thisPID, err = kernel.RootNSPID()
	if err != nil {
		log.Warnf("Watcher Start can't get root namespace pid %s", err)
	}

	_ = kernel.WithAllProcs(w.procRoot, func(pid int) error {
		if pid == w.thisPID { // don't scan ourself
			return nil
		}

		mapsPath := fmt.Sprintf("%s/%d/maps", w.procRoot, pid)
		maps, err := os.Open(mapsPath)
		if err != nil {
			log.Debugf("process %d parsing failed %s", pid, err)
			return nil
		}
		defer maps.Close()

		// Creating a callback to be applied on the paths extracted from the `maps` file.
		// We're creating the callback here, as we need the pid (which varies between iterations).
		parseMapsFileCallback := func(path string) {
			// Iterate over the rule, and look for a match.
			for _, r := range w.rules {
				if r.Re.MatchString(path) {
					_ = w.registry.Register(path, uint32(pid), r.RegisterCB, r.UnregisterCB, utils.IgnoreCB)
					break
				}
			}
		}
		scanner := bufio.NewScanner(bufio.NewReader(maps))
		parseMapsFile(scanner, parseMapsFileCallback)
		return nil
	})

	cleanupExit := w.processMonitor.SubscribeExit(func(pid uint32) { _ = w.registry.Unregister(pid) })
	cleanupLibs, err := w.ebpfProgram.Subscribe(w.handleLibraryOpen, w.libset)
	if err != nil {
		log.Errorf("error subscribing to shared library events: %s", err)
		return
	}

	w.wg.Add(1)
	go func() {
		processSync := time.NewTicker(scanTerminatedProcessesInterval)

		defer func() {
			processSync.Stop()
			// Removing the registration of our hook.
			cleanupExit()
			cleanupLibs()
			// Stopping the process and library monitors (if we're the last instance)
			w.processMonitor.Stop()
			w.ebpfProgram.Stop()
			// Cleaning up all active hooks.
			w.registry.Clear()
			// marking we're finished.
			w.wg.Done()
		}()

		for {
			select {
			case <-w.done:
				return
			case <-processSync.C:
				w.sync()
			}
		}
	}()

	err = w.ebpfProgram.Start()
	if err != nil {
		log.Errorf("error starting shared library detection eBPF program: %s", err)
	}

	utils.AddAttacher(consts.USMModuleName, "native", w)
}

// sync unregisters from any terminated processes which we missed the exit
// callback for, and also attempts to register to running processes to ensure
// that we don't miss any process.
func (w *Watcher) sync() {
	// The mutex is only used for protection with the test code which reads the
	// scannedPIDs map.
	w.syncMutex.Lock()
	defer w.syncMutex.Unlock()

	deletionCandidates := w.registry.GetRegisteredProcesses()
	alivePIDs := make(map[uint32]struct{})

	_ = kernel.WithAllProcs(kernel.ProcFSRoot(), func(origPid int) error {
		if origPid == w.thisPID { // don't scan ourselves
			return nil
		}

		pid := uint32(origPid)
		alivePIDs[pid] = struct{}{}

		if _, ok := deletionCandidates[pid]; ok {
			// We have previously hooked into this process and it remains
			// active, so we remove it from the deletionCandidates list, and
			// move on to the next PID
			delete(deletionCandidates, pid)
			return nil
		}

		scanned := w.scannedPIDs[pid]

		// Try to scan twice. This is because we may happen to scan the process
		// just after it has been exec'd and before it has opened its shared
		// libraries. Scanning twice with the sync interval reduce this risk of
		// missing shared libraries due to this.
		if scanned < 2 {
			w.scannedPIDs[pid]++
			err := w.AttachPID(pid)
			if err == nil {
				log.Debugf("watcher attached to %v via periodic scan", pid)
				w.scannedPIDs[pid] = 2
			}
		}

		return nil
	})

	// Clean up dead processes from the list of scanned PIDs
	for pid := range w.scannedPIDs {
		if _, alive := alivePIDs[pid]; !alive {
			delete(w.scannedPIDs, pid)
		}
	}

	for pid := range deletionCandidates {
		_ = w.registry.Unregister(pid)
	}

	if w.mapsCleaner != nil {
		w.mapsCleaner(alivePIDs)
	}
}
