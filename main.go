// Command sticky-fill-first is a CLIProxyAPI scheduler plugin built as a C shared library.
// See internal/sticky for the routing behavior.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/magoz/cpa-sticky-fill-first/internal/sticky"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// version is set at release build time with -ldflags "-X main.version=<version>".
var version = "dev"

const repository = "https://github.com/magoz/cpa-sticky-fill-first"

var scheduler = sticky.NewScheduler(sticky.DefaultConfig())

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  capabilities       `json:"capabilities"`
}

type capabilities struct {
	Scheduler bool `json:"scheduler"`
	// SchedulerAcrossPriorities lets the plugin see lower-priority credentials, which it
	// needs to stay on a failover credential after a higher-priority one recovers.
	SchedulerAcrossPriorities bool `json:"scheduler_across_priorities"`
}

type hostLogRequest struct {
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if errConfigure := configure(request); errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodSchedulerPick:
		return pick(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return fmt.Errorf("decode lifecycle request: %w", errUnmarshal)
		}
	}
	cfg, errParse := sticky.ParseConfig(req.ConfigYAML)
	if errParse != nil {
		return errParse
	}
	scheduler.Configure(cfg)
	return nil
}

func pick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, fmt.Errorf("decode scheduler request: %w", errUnmarshal)
	}
	resp, moved := scheduler.Pick(req, time.Now())
	if moved != nil {
		logSwitch(*moved)
	}
	return okEnvelope(resp)
}

func logSwitch(moved sticky.Switch) {
	// Best effort: a failed host log call must not affect routing.
	// The host log formatter prints only well-known field names, so the details go in the message.
	_ = hostLog(hostLogRequest{
		Level: "info",
		Message: fmt.Sprintf("sticky-fill-first: pin moved from %s to %s after %s unavailable",
			moved.From, moved.To, moved.MissingFor.Round(time.Second)),
		Fields: map[string]any{
			"provider": moved.Provider,
			"model":    moved.Model,
		},
	})
}

func hostLog(req hostLogRequest) error {
	payload, errMarshal := json.Marshal(req)
	if errMarshal != nil {
		return errMarshal
	}
	method := C.CString(pluginabi.MethodHostLog)
	defer C.free(unsafe.Pointer(method))
	body := C.CBytes(payload)
	defer C.free(body)

	var response C.cliproxy_buffer
	status := C.call_host_api(method, (*C.uint8_t)(body), C.size_t(len(payload)), &response)
	C.free_host_buffer(response.ptr, response.len)
	if status != 0 {
		return errors.New("host.log call failed")
	}
	return nil
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "Sticky Fill First",
			Version:          version,
			Author:           "magoz",
			GitHubRepository: repository,
			ConfigFields: []pluginapi.ConfigField{
				{
					Name:        "providers",
					Type:        pluginapi.ConfigFieldTypeArray,
					Description: "Provider keys to route stickily, for example claude and codex. Empty applies to every provider.",
				},
				{
					Name:        "switch_after",
					Type:        pluginapi.ConfigFieldTypeString,
					Description: "How long the pinned credential must stay unavailable before the pin moves (Go duration, default 5m).",
				},
			},
		},
		Capabilities: capabilities{
			Scheduler:                 true,
			SchedulerAcrossPriorities: true,
		},
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
