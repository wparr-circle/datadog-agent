// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux_bpf

package usm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	manager "github.com/DataDog/ebpf-manager"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/features"
	"github.com/davecgh/go-spew/spew"

	ddebpf "github.com/DataDog/datadog-agent/pkg/ebpf"
	"github.com/DataDog/datadog-agent/pkg/network/config"
	"github.com/DataDog/datadog-agent/pkg/network/go/bininspect"
	"github.com/DataDog/datadog-agent/pkg/network/protocols"
	"github.com/DataDog/datadog-agent/pkg/network/protocols/http"
	"github.com/DataDog/datadog-agent/pkg/network/usm/buildmode"
	usmconfig "github.com/DataDog/datadog-agent/pkg/network/usm/config"
	"github.com/DataDog/datadog-agent/pkg/network/usm/sharedlibraries"
	"github.com/DataDog/datadog-agent/pkg/network/usm/utils"
	"github.com/DataDog/datadog-agent/pkg/util/common"
	"github.com/DataDog/datadog-agent/pkg/util/kernel"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/util/safeelf"
	ddsync "github.com/DataDog/datadog-agent/pkg/util/sync"
)

const (
	sslReadExProbe              = "uprobe__SSL_read_ex"
	sslReadExRetprobe           = "uretprobe__SSL_read_ex"
	sslWriteExProbe             = "uprobe__SSL_write_ex"
	sslWriteExRetprobe          = "uretprobe__SSL_write_ex"
	sslDoHandshakeProbe         = "uprobe__SSL_do_handshake"
	sslDoHandshakeRetprobe      = "uretprobe__SSL_do_handshake"
	sslConnectProbe             = "uprobe__SSL_connect"
	sslConnectRetprobe          = "uretprobe__SSL_connect"
	sslSetBioProbe              = "uprobe__SSL_set_bio"
	sslSetFDProbe               = "uprobe__SSL_set_fd"
	sslReadProbe                = "uprobe__SSL_read"
	sslReadRetprobe             = "uretprobe__SSL_read"
	sslWriteProbe               = "uprobe__SSL_write"
	sslWriteRetprobe            = "uretprobe__SSL_write"
	sslShutdownProbe            = "uprobe__SSL_shutdown"
	bioNewSocketProbe           = "uprobe__BIO_new_socket"
	bioNewSocketRetprobe        = "uretprobe__BIO_new_socket"
	gnutlsHandshakeProbe        = "uprobe__gnutls_handshake"
	gnutlsHandshakeRetprobe     = "uretprobe__gnutls_handshake"
	gnutlsTransportSetInt2Probe = "uprobe__gnutls_transport_set_int2"
	gnutlsTransportSetPtrProbe  = "uprobe__gnutls_transport_set_ptr"
	gnutlsTransportSetPtr2Probe = "uprobe__gnutls_transport_set_ptr2"
	gnutlsRecordRecvProbe       = "uprobe__gnutls_record_recv"
	gnutlsRecordRecvRetprobe    = "uretprobe__gnutls_record_recv"
	gnutlsRecordSendProbe       = "uprobe__gnutls_record_send"
	gnutlsRecordSendRetprobe    = "uretprobe__gnutls_record_send"
	gnutlsByeProbe              = "uprobe__gnutls_bye"
	gnutlsDeinitProbe           = "uprobe__gnutls_deinit"

	rawTracepointSchedProcessExit = "raw_tracepoint__sched_process_exit"
	oldTracepointSchedProcessExit = "tracepoint__sched__sched_process_exit"
)

var openSSLProbes = []manager.ProbesSelector{
	&manager.BestEffort{
		Selectors: []manager.ProbesSelector{
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslReadExProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslReadExRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslWriteExProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslWriteExRetprobe,
				},
			},
		},
	},
	&manager.AllOf{
		Selectors: []manager.ProbesSelector{
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslDoHandshakeProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslDoHandshakeRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslConnectProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslConnectRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslSetBioProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslSetFDProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslReadProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslReadRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslWriteProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslWriteRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: sslShutdownProbe,
				},
			},
		},
	},
}

var cryptoProbes = []manager.ProbesSelector{
	&manager.AllOf{
		Selectors: []manager.ProbesSelector{
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: bioNewSocketProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: bioNewSocketRetprobe,
				},
			},
		},
	},
}

var gnuTLSProbes = []manager.ProbesSelector{
	&manager.AllOf{
		Selectors: []manager.ProbesSelector{
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsHandshakeProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsHandshakeRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsTransportSetInt2Probe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsTransportSetPtrProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsTransportSetPtr2Probe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsRecordRecvProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsRecordRecvRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsRecordSendProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsRecordSendRetprobe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsByeProbe,
				},
			},
			&manager.ProbeSelector{
				ProbeIdentificationPair: manager.ProbeIdentificationPair{
					EBPFFuncName: gnutlsDeinitProbe,
				},
			},
		},
	},
}

const (
	sslSockByCtxMap    = "ssl_sock_by_ctx"
	sslCtxByPIDTGIDMap = "ssl_ctx_by_pid_tgid"
)

var (
	buildKitProcessName = []byte("buildkitd")

	sharedLibrariesMaps = []*manager.Map{
		{
			Name: sslSockByCtxMap,
		},
		{
			Name: "ssl_read_args",
		},
		{
			Name: "ssl_read_ex_args",
		},
		{
			Name: "ssl_write_args",
		},
		{
			Name: "ssl_write_ex_args",
		},
		{
			Name: "bio_new_socket_args",
		},
		{
			Name: "fd_by_ssl_bio",
		},
		{
			Name: sslCtxByPIDTGIDMap,
		},
	}
)

// Template, will be modified during runtime.
// The constructor of SSLProgram requires more parameters than we provide in the general way, thus we need to have
// a dynamic initialization.
var opensslSpec = &protocols.ProtocolSpec{
	Factory: newSSLProgramProtocolFactory,
	Maps:    sharedLibrariesMaps,
	Probes: []*manager.Probe{
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: "kprobe__tcp_sendmsg",
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslReadExProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslReadExRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslWriteExProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslWriteExRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslDoHandshakeProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslDoHandshakeRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslConnectProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslConnectRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslSetBioProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslSetFDProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslReadProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslReadRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslWriteProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslWriteRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: sslShutdownProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: bioNewSocketProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: bioNewSocketRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsHandshakeProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsHandshakeRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsTransportSetInt2Probe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsTransportSetPtrProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsTransportSetPtr2Probe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsRecordRecvProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsRecordRecvRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsRecordSendProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsRecordSendRetprobe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsByeProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: gnutlsDeinitProbe,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: rawTracepointSchedProcessExit,
			},
		},
		{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: oldTracepointSchedProcessExit,
			},
		},
	},
}

type sslProgram struct {
	cfg         *config.Config
	watcher     *sharedlibraries.Watcher
	ebpfManager *manager.Manager
}

func newSSLProgramProtocolFactory(m *manager.Manager, c *config.Config) (protocols.Protocol, error) {
	if !c.EnableNativeTLSMonitoring || !usmconfig.TLSSupported(c) {
		return nil, nil
	}

	var (
		watcher *sharedlibraries.Watcher
		err     error
	)
	sslProgram := &sslProgram{
		cfg:         c,
		ebpfManager: m,
	}
	var cleanerCB func(map[uint32]struct{})
	if features.HaveProgramType(ebpf.RawTracepoint) != nil {
		cleanerCB = sslProgram.cleanupDeadPids
	}
	procRoot := kernel.ProcFSRoot()

	if c.EnableNativeTLSMonitoring && usmconfig.TLSSupported(c) {
		watcher, err = sharedlibraries.NewWatcher(c, sharedlibraries.LibsetCrypto, cleanerCB,
			sharedlibraries.Rule{
				Re:           regexp.MustCompile(`libssl.so`),
				RegisterCB:   addHooks(m, procRoot, openSSLProbes),
				UnregisterCB: removeHooks(m, openSSLProbes),
			},
			sharedlibraries.Rule{
				Re:           regexp.MustCompile(`libcrypto.so`),
				RegisterCB:   addHooks(m, procRoot, cryptoProbes),
				UnregisterCB: removeHooks(m, cryptoProbes),
			},
			sharedlibraries.Rule{
				Re:           regexp.MustCompile(`libgnutls.so`),
				RegisterCB:   addHooks(m, procRoot, gnuTLSProbes),
				UnregisterCB: removeHooks(m, gnuTLSProbes),
			},
		)
		if err != nil {
			return nil, fmt.Errorf("error initializing shared library watcher: %s", err)
		}
	}

	sslProgram.watcher = watcher

	return sslProgram, nil
}

// Name return the program's name.
func (o *sslProgram) Name() string {
	return "openssl"
}

func sharedLibrariesConfigureOptions(options *manager.Options, cfg *config.Config) {
	options.MapSpecEditors[sslSockByCtxMap] = manager.MapSpecEditor{
		MaxEntries: cfg.MaxTrackedConnections,
		EditorFlag: manager.EditMaxEntries,
	}
	options.MapSpecEditors[sslCtxByPIDTGIDMap] = manager.MapSpecEditor{
		MaxEntries: cfg.MaxTrackedConnections,
		EditorFlag: manager.EditMaxEntries,
	}
	options.ActivatedProbes = append(options.ActivatedProbes, &manager.ProbeSelector{
		ProbeIdentificationPair: manager.ProbeIdentificationPair{EBPFFuncName: "kprobe__tcp_sendmsg"},
	})
}

// ConfigureOptions changes map attributes to the given options.
func (o *sslProgram) ConfigureOptions(options *manager.Options) {
	sharedLibrariesConfigureOptions(options, o.cfg)
	o.addProcessExitProbe(options)
}

// PreStart is called before the start of the provided eBPF manager.
func (o *sslProgram) PreStart() error {
	o.watcher.Start()
	return nil
}

// PostStart is a no-op.
func (o *sslProgram) PostStart() error {
	return nil
}

// Stop stops the program.
func (o *sslProgram) Stop() {
	o.watcher.Stop()
}

// DumpMaps dumps the content of the map represented by mapName & currentMap, if it used by the eBPF program, to output.
func (o *sslProgram) DumpMaps(w io.Writer, mapName string, currentMap *ebpf.Map) {
	switch mapName {
	case sslSockByCtxMap: // maps/ssl_sock_by_ctx (BPF_MAP_TYPE_HASH), key uintptr // C.void *, value C.ssl_sock_t
		io.WriteString(w, "Map: '"+mapName+"', key: 'uintptr // C.void *', value: 'C.ssl_sock_t'\n")
		iter := currentMap.Iterate()
		var key uintptr // C.void *
		var value http.SslSock
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "ssl_read_args": // maps/ssl_read_args (BPF_MAP_TYPE_HASH), key C.__u64, value C.ssl_read_args_t
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'C.ssl_read_args_t'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value http.SslReadArgs
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "ssl_read_ex_args": // maps/ssl_read_ex_args (BPF_MAP_TYPE_HASH), key C.__u64, value C.ssl_read_ex_args_t
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'C.ssl_read_ex_args_t'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value http.SslReadExArgs
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "ssl_write_args": // maps/ssl_write_args (BPF_MAP_TYPE_HASH), key C.__u64, value C.ssl_write_args_t
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'C.ssl_write_args_t'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value http.SslWriteArgs
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "ssl_write_ex_args_t": // maps/ssl_write_ex_args_t (BPF_MAP_TYPE_HASH), key C.__u64, value C.ssl_write_args_t
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'C.ssl_write_ex_args_t'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value http.SslWriteExArgs
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "bio_new_socket_args": // maps/bio_new_socket_args (BPF_MAP_TYPE_HASH), key C.__u64, value C.__u32
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'C.__u32'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value uint32
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case "fd_by_ssl_bio": // maps/fd_by_ssl_bio (BPF_MAP_TYPE_HASH), key C.__u32, value uintptr // C.void *
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u32', value: 'uintptr // C.void *'\n")
		iter := currentMap.Iterate()
		var key uint32
		var value uintptr // C.void *
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}

	case sslCtxByPIDTGIDMap: // maps/ssl_ctx_by_pid_tgid (BPF_MAP_TYPE_HASH), key C.__u64, value uintptr // C.void *
		io.WriteString(w, "Map: '"+mapName+"', key: 'C.__u64', value: 'uintptr // C.void *'\n")
		iter := currentMap.Iterate()
		var key uint64
		var value uintptr // C.void *
		for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
			spew.Fdump(w, key, value)
		}
	}

}

// GetStats is a no-op.
func (o *sslProgram) GetStats() (*protocols.ProtocolStats, func()) {
	return nil, nil
}

const (
	// Defined in https://man7.org/linux/man-pages/man5/proc.5.html.
	taskCommLen = 16
)

var (
	taskCommLenBufferPool = ddsync.NewSlicePool[byte](taskCommLen, taskCommLen)
)

func isContainerdTmpMount(path string) bool {
	return strings.Contains(path, "tmpmounts/containerd-mount") || strings.Contains(path, "/tmp/ctd-volume")
}

func isBuildKit(procRoot string, pid uint32) bool {
	filePath := filepath.Join(procRoot, strconv.Itoa(int(pid)), "comm")

	file, err := os.Open(filePath)
	if err != nil {
		// Waiting a bit, as we might get the event of process creation before the directory was created.
		for i := 0; i < 30; i++ {
			time.Sleep(1 * time.Millisecond)
			// reading again.
			file, err = os.Open(filePath)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return false
	}
	defer file.Close()

	buf := taskCommLenBufferPool.Get()
	defer taskCommLenBufferPool.Put(buf)
	n, err := file.Read(*buf)
	if err != nil {
		// short living process can hit here, or slow start of another process.
		return false
	}
	return bytes.Equal(bytes.TrimSpace((*buf)[:n]), buildKitProcessName)
}

func addHooks(m *manager.Manager, procRoot string, probes []manager.ProbesSelector) func(utils.FilePath) error {
	return func(fpath utils.FilePath) error {
		if isBuildKit(procRoot, fpath.PID) {
			return fmt.Errorf("%w: process %d is buildkitd, skipping", utils.ErrEnvironment, fpath.PID)
		} else if isContainerdTmpMount(fpath.HostPath) {
			return fmt.Errorf("%w: path %s from process %d is tempmount of containerd, skipping", utils.ErrEnvironment, fpath.HostPath, fpath.PID)
		}

		uid := getUID(fpath.ID)

		elfFile, err := safeelf.Open(fpath.HostPath)
		if err != nil {
			return err
		}
		defer elfFile.Close()

		// This only allows amd64 and arm64 and not the 32-bit variants, but that
		// is fine since we don't monitor 32-bit applications at all in the shared
		// library watcher since compat syscalls aren't supported by the syscall
		// trace points.  We do actually monitor 32-bit applications for istio and
		// nodejs monitoring, but our uprobe hooks only properly support 64-bit
		// applications, so there's no harm in rejecting 32-bit applications here.
		arch, err := bininspect.GetArchitecture(elfFile)
		if err != nil {
			return err
		}

		// Ignore foreign architectures.  This can happen when running stuff under
		// qemu-user, for example, and installing a uprobe will lead to segfaults
		// since the foreign instructions will be patched with the native break
		// instruction.
		if string(arch) != runtime.GOARCH {
			return fmt.Errorf("unspported architecture: %s", arch)
		}

		symbolsSet := make(common.StringSet)
		symbolsSetBestEffort := make(common.StringSet)
		for _, singleProbe := range probes {
			_, isBestEffort := singleProbe.(*manager.BestEffort)
			for _, selector := range singleProbe.GetProbesIdentificationPairList() {
				_, symbol, ok := strings.Cut(selector.EBPFFuncName, "__")
				if !ok {
					continue
				}
				if isBestEffort {
					symbolsSetBestEffort[symbol] = struct{}{}
				} else {
					symbolsSet[symbol] = struct{}{}
				}
			}
		}
		symbolMap, err := bininspect.GetAllSymbolsInSetByName(elfFile, symbolsSet)
		if err != nil {
			return err
		}
		/* Best effort to resolve symbols, so we don't care about the error */
		symbolMapBestEffort, _ := bininspect.GetAllSymbolsInSetByName(elfFile, symbolsSetBestEffort)

		for _, singleProbe := range probes {
			_, isBestEffort := singleProbe.(*manager.BestEffort)
			for _, selector := range singleProbe.GetProbesIdentificationPairList() {
				identifier := manager.ProbeIdentificationPair{
					EBPFFuncName: selector.EBPFFuncName,
					UID:          uid,
				}
				singleProbe.EditProbeIdentificationPair(selector, identifier)
				probe, found := m.GetProbe(identifier)
				if found {
					if !probe.IsRunning() {
						err := probe.Attach()
						if err != nil {
							return err
						}
					}

					continue
				}

				_, symbol, ok := strings.Cut(selector.EBPFFuncName, "__")
				if !ok {
					continue
				}

				sym := symbolMap[symbol]
				if isBestEffort {
					sym, found = symbolMapBestEffort[symbol]
					if !found {
						continue
					}
				}
				manager.SanitizeUprobeAddresses(elfFile.File, []safeelf.Symbol{sym})
				offset, err := bininspect.SymbolToOffset(elfFile, sym)
				if err != nil {
					return err
				}

				newProbe := &manager.Probe{
					ProbeIdentificationPair: identifier,
					BinaryPath:              fpath.HostPath,
					UprobeOffset:            uint64(offset),
					HookFuncName:            symbol,
				}
				if err := m.AddHook("", newProbe); err == nil {
					ddebpf.AddProgramNameMapping(newProbe.ID(), newProbe.EBPFFuncName, "usm_tls")
				}
			}
			if err := singleProbe.RunValidator(m); err != nil {
				return err
			}
		}

		return nil
	}
}

func removeHooks(m *manager.Manager, probes []manager.ProbesSelector) func(utils.FilePath) error {
	return func(fpath utils.FilePath) error {
		uid := getUID(fpath.ID)
		for _, singleProbe := range probes {
			for _, selector := range singleProbe.GetProbesIdentificationPairList() {
				identifier := manager.ProbeIdentificationPair{
					EBPFFuncName: selector.EBPFFuncName,
					UID:          uid,
				}
				probe, found := m.GetProbe(identifier)
				if !found {
					continue
				}

				program := probe.Program()
				err := m.DetachHook(identifier)
				if err != nil {
					log.Debugf("detach hook %s/%s : %s", selector.EBPFFuncName, uid, err)
				}
				if program != nil {
					program.Close()
				}
			}
		}

		return nil
	}
}

// getUID() return a key of length 5 as the kernel uprobe registration path is limited to a length of 64
// ebpf-manager/utils.go:GenerateEventName() MaxEventNameLen = 64
// MAX_EVENT_NAME_LEN (linux/kernel/trace/trace.h)
//
// Length 5 is arbitrary value as the full string of the eventName format is
//
//	fmt.Sprintf("%s_%.*s_%s_%s", probeType, maxFuncNameLen, functionName, UID, attachPIDstr)
//
// functionName is variable but with a minimum guarantee of 10 chars
func getUID(lib utils.PathIdentifier) string {
	return lib.Key()[:5]
}

// IsBuildModeSupported returns always true, as tls module is supported by all modes.
func (*sslProgram) IsBuildModeSupported(buildmode.Type) bool {
	return true
}

// addProcessExitProbe adds a raw or regular tracepoint program depending on which is supported.
func (o *sslProgram) addProcessExitProbe(options *manager.Options) {
	if features.HaveProgramType(ebpf.RawTracepoint) == nil {
		// use a raw tracepoint on a supported kernel to intercept terminated threads and clear the corresponding maps
		p := &manager.Probe{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: rawTracepointSchedProcessExit,
				UID:          probeUID,
			},
			TracepointName: "sched_process_exit",
		}
		o.ebpfManager.Probes = append(o.ebpfManager.Probes, p)
		options.ActivatedProbes = append(options.ActivatedProbes, &manager.ProbeSelector{ProbeIdentificationPair: p.ProbeIdentificationPair})
		// exclude regular tracepoint
		options.ExcludedFunctions = append(options.ExcludedFunctions, oldTracepointSchedProcessExit)
	} else {
		// use a regular tracepoint to intercept terminated threads
		p := &manager.Probe{
			ProbeIdentificationPair: manager.ProbeIdentificationPair{
				EBPFFuncName: oldTracepointSchedProcessExit,
				UID:          probeUID,
			},
		}
		o.ebpfManager.Probes = append(o.ebpfManager.Probes, p)
		options.ActivatedProbes = append(options.ActivatedProbes, &manager.ProbeSelector{ProbeIdentificationPair: p.ProbeIdentificationPair})
		// exclude a raw tracepoint
		options.ExcludedFunctions = append(options.ExcludedFunctions, rawTracepointSchedProcessExit)
	}
}

var sslPidKeyMaps = []string{
	"ssl_read_args",
	"ssl_read_ex_args",
	"ssl_write_args",
	"ssl_write_ex_args",
	"ssl_ctx_by_pid_tgid",
	"bio_new_socket_args",
}

// cleanupDeadPids clears maps of terminated processes, is invoked when raw tracepoints unavailable.
func (o *sslProgram) cleanupDeadPids(alivePIDs map[uint32]struct{}) {
	for _, mapName := range sslPidKeyMaps {
		err := deleteDeadPidsInMap(o.ebpfManager, mapName, alivePIDs)
		if err != nil {
			log.Debugf("SSL map %q cleanup error: %v", mapName, err)
		}
	}
}

// deleteDeadPidsInMap finds a map by name and deletes dead processes.
// enters when raw tracepoint is not supported, kernel < 4.17
func deleteDeadPidsInMap(manager *manager.Manager, mapName string, alivePIDs map[uint32]struct{}) error {
	emap, _, err := manager.GetMap(mapName)
	if err != nil {
		return fmt.Errorf("dead process cleaner failed to get map: %q error: %w", mapName, err)
	}

	var keysToDelete []uint64
	var key uint64
	value := make([]byte, emap.ValueSize())
	iter := emap.Iterate()

	for iter.Next(unsafe.Pointer(&key), unsafe.Pointer(&value)) {
		pid := uint32(key >> 32)
		if _, exists := alivePIDs[pid]; !exists {
			keysToDelete = append(keysToDelete, key)
		}
	}
	for _, k := range keysToDelete {
		_ = emap.Delete(unsafe.Pointer(&k))
	}

	return nil
}
