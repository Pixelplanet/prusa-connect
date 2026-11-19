package main

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// declaredOptionKeys reads the option keys out of module.yaml: every
// `- key:` line between `options:` and the next key at the left margin.
func declaredOptionKeys(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatalf("read module.yaml: %v", err)
	}
	var keys []string
	inside := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "options:") {
			inside = true
			continue
		}
		if inside && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			inside = false
		}
		if key, found := strings.CutPrefix(strings.TrimSpace(line), "- key:"); inside && found {
			keys = append(keys, strings.TrimSpace(key))
		}
	}
	if len(keys) == 0 {
		t.Fatal("module.yaml declares no option; the reader above no longer matches the file")
	}
	return keys
}

// The host decodes plugin.configure strictly. An option module.yaml declares
// and the settings type has no field for refuses the start as soon as an
// operator writes it, so every declared key is sent here.
func TestConfigureAcceptsEveryDeclaredOption(t *testing.T) {
	config := map[string]string{}
	for _, key := range declaredOptionKeys(t) {
		config[key] = "192.168.1.20"
	}
	raw, err := json.Marshal(map[string]any{"config": config})
	if err != nil {
		t.Fatal(err)
	}

	sent, err := wirerpc.ReadSettings[hostSettings](raw)
	if err != nil {
		t.Fatalf("plugin.configure refused a declared option: %v", err)
	}
	if sent.MQTTHost != "192.168.1.20" || sent.HTTPHost != "192.168.1.20" || sent.HAURL != "192.168.1.20" {
		t.Fatalf("declared options were not read: %+v", sent)
	}

	// The type 0.4.1 decoded into has no http_host, which is what this guards.
	if _, err = wirerpc.ReadSettings[Settings](raw); err == nil {
		t.Fatal("Settings accepted http_host; hostSettings is no longer needed for it")
	}
}

func TestHostPinsWinOverSavedSettings(t *testing.T) {
	m := newModule(nil)
	saved := normalizeSettings(Settings{MQTTHost: "10.0.0.5", MQTTPort: 1884, HAURL: "http://old:8123"})

	if got := m.applyHostPins(saved); got.MQTTHost != "10.0.0.5" || got.HAURL != "http://old:8123" {
		t.Fatalf("settings changed with nothing pinned: %+v", got)
	}

	m.setHostConfig(hostSettings{
		Settings: Settings{MQTTHost: " broker.lan ", HAURL: "http://ha.lan:8123/"},
		HTTPHost: "192.168.1.30",
	}, wirerpc.Grants{})

	got := m.applyHostPins(saved)
	if got.MQTTHost != "broker.lan" {
		t.Errorf("mqtt host = %q, want the pinned broker.lan", got.MQTTHost)
	}
	if got.MQTTPort != 1884 {
		t.Errorf("mqtt port = %d, want the saved 1884 kept", got.MQTTPort)
	}
	if got.HAURL != "http://ha.lan:8123" {
		t.Errorf("ha url = %q, want the pinned http://ha.lan:8123", got.HAURL)
	}

	resp := m.buildSettingsResponse()
	if !resp.MQTTHostPinned || !resp.HAURLPinned {
		t.Errorf("pins not reported: %+v", resp)
	}
	if resp.SuggestedHTTPHost != "192.168.1.30" {
		t.Errorf("suggested http host = %q", resp.SuggestedHTTPHost)
	}
}

// A new install has saved nothing and is sent nothing. It runs on the
// defaults the code names, never on the zero value of each setting.
func TestNewInstallStartsOnTheDefaults(t *testing.T) {
	m := newModule(nil)
	m.setDataDir(t.TempDir())

	got := m.startSettings(hostSettings{})
	if got.AutoOffCooldownSec != defaultCooldownSec {
		t.Errorf("auto-off delay = %d s, want the default %d s", got.AutoOffCooldownSec, defaultCooldownSec)
	}
	if !got.confirmTurnOffEnabled() {
		t.Error("a new install does not ask before turning off")
	}
	if got.MQTTPort != defaultMQTTPort || got.MQTTClientID != defaultMQTTClientID {
		t.Errorf("broker defaults not applied: %+v", got)
	}

	saved := normalizeSettings(Settings{MQTTHost: "10.0.0.5", AutoOffCooldownSec: 0})
	if err := m.writeSettingsToDisk(saved); err != nil {
		t.Fatal(err)
	}
	if got = m.startSettings(hostSettings{}); got.AutoOffCooldownSec != 0 || got.MQTTHost != "10.0.0.5" {
		t.Errorf("saved settings were not kept: %+v", got)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err = ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestBuiltinBrokerListensOnlyOnGrantedPort(t *testing.T) {
	m := newModule(nil)
	s := normalizeSettings(Settings{MQTTBuiltin: true})

	m.reconnectMQTT(s)
	m.mu.RLock()
	brk, errMsg := m.embeddedBroker, m.mqttError
	m.mu.RUnlock()
	if brk != nil {
		_ = brk.Close()
		t.Fatal("the built-in broker started with no port granted")
	}
	if !strings.Contains(errMsg, "not approved") {
		t.Fatalf("status = %q, want it to say the port is not approved", errMsg)
	}
	if resp := m.buildSettingsResponse(); resp.MQTTBuiltinGranted {
		t.Fatal("an ungranted port was reported as granted")
	}

	port := freeTCPPort(t)
	m.setHostConfig(hostSettings{}, wirerpc.Grants{
		Listen: map[string]wirerpc.ListenGrant{builtinListenName: {Network: "tcp", Port: port}},
	})
	m.reconnectMQTT(s)
	defer m.disconnectMQTT()

	resp := m.buildSettingsResponse()
	if !resp.MQTTBuiltinRunning || resp.MQTTBuiltinPort != port || !resp.MQTTBuiltinGranted {
		t.Fatalf("built-in broker not running on the granted port %d: %+v", port, resp)
	}
}

func TestOverviewKeepsToVisiblePrinters(t *testing.T) {
	m := newModule(nil)
	m.mu.Lock()
	m.configs["printer-a"] = DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug_a", AutoOffAfterPrint: true}
	m.configs["printer-b"] = DeviceConfig{Driver: DriverTasmotaHTTP, HTTPHost: "192.168.1.40", HTTPPassword: "secret"}
	m.configs["printer-c"] = DeviceConfig{}
	m.mu.Unlock()

	all := m.buildOverview(func(string) bool { return true })
	if len(all.Devices) != 3 {
		t.Fatalf("overview has %d printers, want 3", len(all.Devices))
	}
	if all.Devices[0].DeviceID != "printer-a" || all.Devices[2].DeviceID != "printer-c" {
		t.Fatalf("overview is not in a stable order: %+v", all.Devices)
	}
	if all.Devices[0].Plug == nil || all.Devices[0].Plug.Power != "UNKNOWN" {
		t.Errorf("a plug nothing was heard from should read UNKNOWN: %+v", all.Devices[0].Plug)
	}
	if all.Devices[1].Config.HTTPPassword != "" || !all.Devices[1].Config.HasHTTPPassword {
		t.Errorf("the plug password left the module: %+v", all.Devices[1].Config)
	}
	if all.Devices[2].Plug != nil {
		t.Errorf("a printer with no plug has one: %+v", all.Devices[2].Plug)
	}

	some := m.buildOverview(func(id string) bool { return id == "printer-b" })
	if len(some.Devices) != 1 || some.Devices[0].DeviceID != "printer-b" {
		t.Fatalf("overview named a printer the caller may not see: %+v", some.Devices)
	}
}
