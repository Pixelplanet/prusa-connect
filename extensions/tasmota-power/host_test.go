package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

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
	s := normalizeSettings(Settings{MQTTBuiltin: true, MQTTUsername: "plugs", MQTTPassword: "secret"})

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

	all := m.buildOverview(overviewOptions{visible: func(string) bool { return true }})
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

	some := m.buildOverview(overviewOptions{visible: func(id string) bool { return id == "printer-b" }})
	if len(some.Devices) != 1 || some.Devices[0].DeviceID != "printer-b" {
		t.Fatalf("overview named a printer the caller may not see: %+v", some.Devices)
	}
}

// grantedModule is a module granted a free port for the built-in broker.
func grantedModule(t *testing.T) (*module, int) {
	t.Helper()
	m := newModule(nil)
	m.setDataDir(t.TempDir())
	port := freeTCPPort(t)
	m.setHostConfig(hostSettings{}, wirerpc.Grants{
		Listen: map[string]wirerpc.ListenGrant{builtinListenName: {Network: "tcp", Port: port}},
	})
	return m, port
}

func TestTheBuiltinBrokerDoesNotStartWithoutCredentials(t *testing.T) {
	for _, creds := range [][2]string{{"", ""}, {"plugs", ""}, {"", "secret"}, {"  ", "secret"}} {
		if brk, err := StartEmbeddedBroker("127.0.0.1:0", creds[0], creds[1]); err == nil {
			_ = brk.Close()
			t.Fatalf("started with user %q and password %q", creds[0], creds[1])
		}
	}

	m, _ := grantedModule(t)
	m.reconnectMQTT(normalizeSettings(Settings{MQTTBuiltin: true}))
	defer m.disconnectMQTT()
	resp := m.buildSettingsResponse()
	if resp.MQTTBuiltinRunning {
		t.Fatal("the built-in broker started without a user name and a password")
	}
	if !strings.Contains(resp.MQTTError, "user name and a password") {
		t.Fatalf("mqtt_error = %q, want it to ask for a user name and a password", resp.MQTTError)
	}
}

func TestTheBuiltinBrokerRefusesAClientThatDoesNotSignIn(t *testing.T) {
	brk, err := StartEmbeddedBroker("127.0.0.1:0", "plugs", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Close() }()

	url := fmt.Sprintf("tcp://127.0.0.1:%d", brk.Port())
	for _, tc := range []struct{ name, user, pass string }{
		{"anonymous", "", ""},
		{"no password", "plugs", ""},
		{"wrong password", "plugs", "guess"},
		{"wrong user", "other", "secret"},
	} {
		opts := mqtt.NewClientOptions().AddBroker(url).SetClientID("c-" + strings.ReplaceAll(tc.name, " ", "-"))
		if tc.user != "" {
			opts.SetUsername(tc.user)
		}
		if tc.pass != "" {
			opts.SetPassword(tc.pass)
		}
		if err := tryConnect(opts); err == nil {
			t.Errorf("%s: the broker let the client in", tc.name)
		}
	}
	ok := mqtt.NewClientOptions().AddBroker(url).SetClientID("c-ok").SetUsername("plugs").SetPassword("secret")
	if err := tryConnect(ok); err != nil {
		t.Fatalf("the right user name and password were refused: %v", err)
	}
}

func TestSavingTheBuiltinBrokerWithoutCredentialsIsRefused(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionConfigureResource: true}}
	m := guardedModule(t, "user:manager", access)

	rec := serve(m, "PUT", "/settings", `{"mqtt_builtin":true,"mqtt_username":"plugs"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); !strings.HasSuffix(code, "INVALID_INPUT") {
		t.Fatalf("code %q, want INVALID_INPUT", code)
	}
	if m.settings().MQTTBuiltin {
		t.Fatal("the refused settings were kept")
	}
}

// The module's own client reaches the built-in broker over a pipe, so the
// broker counts no accepted connection and no loopback socket is opened.
func TestTheModuleReachesTheBuiltinBrokerInProcess(t *testing.T) {
	m, port := grantedModule(t)
	s := normalizeSettings(Settings{MQTTBuiltin: true, MQTTUsername: "plugs", MQTTPassword: "secret"})
	m.reconnectMQTT(s)
	defer m.disconnectMQTT()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := m.buildSettingsResponse()
		if resp.MQTTConnected {
			if resp.MQTTBuiltinPort != port || resp.MQTTBuiltinClients != 1 {
				t.Fatalf("connected, but not to the built-in broker on %d: %+v", port, resp)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the module did not connect to its built-in broker: %+v", resp)
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.mu.RLock()
	brk := m.embeddedBroker
	m.mu.RUnlock()
	if n := brk.AcceptedCount(); n != 0 {
		t.Fatalf("the broker accepted %d TCP connections; the module's client should use the pipe", n)
	}
}

// Testing the built-in broker opens no listener but the granted port.
func TestTestingTheBuiltinBrokerListensOnlyOnTheGrantedPort(t *testing.T) {
	m, port := grantedModule(t)
	saved := normalizeSettings(Settings{MQTTBuiltin: true, MQTTUsername: "plugs", MQTTPassword: "secret"})
	m.held.Store(&saved)

	// Not running: the test starts the broker on the granted port for the test.
	ok, broker, errMsg, _ := m.testBroker(nil)
	if !ok || !strings.Contains(broker, fmt.Sprint(port)) {
		t.Fatalf("test of a stopped broker: ok=%v broker=%q err=%q", ok, broker, errMsg)
	}

	m.reconnectMQTT(saved)
	defer m.disconnectMQTT()

	// Running, with other credentials in the form: no second broker, and the
	// answer says the new values are not in use yet.
	other := saved
	other.MQTTPassword = "changed"
	ok, _, errMsg, warn := m.testBroker(&other)
	if !ok || !strings.Contains(warn, "Save") {
		t.Fatalf("test with unsaved credentials: ok=%v err=%q warning=%q", ok, errMsg, warn)
	}

	// No credentials: refused without listening.
	none := saved
	none.MQTTUsername, none.MQTTPassword = "", ""
	if ok, _, errMsg, _ := m.testBroker(&none); ok || !strings.Contains(errMsg, "user name and a password") {
		t.Fatalf("test without credentials: ok=%v err=%q", ok, errMsg)
	}

	// No port granted: refused without listening.
	m.setHostConfig(hostSettings{}, wirerpc.Grants{})
	if ok, _, errMsg, _ := m.testBroker(nil); ok || !strings.Contains(errMsg, "not approved") {
		t.Fatalf("test without a granted port: ok=%v err=%q", ok, errMsg)
	}
}

func TestParseBrokerURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		host string
		port int
		tls  bool
	}{
		{"192.168.1.5", "192.168.1.5", 0, false},
		{"broker.lan:1884", "broker.lan", 1884, false},
		{"tcp://broker.lan:1883", "broker.lan", 1883, false},
		{"mqtts://broker.lan", "broker.lan", 0, true},
		{"ssl://user:pw@broker.lan:8883/", "broker.lan", 8883, true},
		{"[fd00::5]:1883", "fd00::5", 1883, false},
	} {
		host, port, useTLS := parseBrokerURL(tc.raw)
		if host != tc.host || port != tc.port || useTLS != tc.tls {
			t.Errorf("parseBrokerURL(%q) = %q %d %v, want %q %d %v", tc.raw, host, port, useTLS, tc.host, tc.port, tc.tls)
		}
	}
}

// An install from before 0.5.0 runs the built-in broker without credentials.
// It may still save its other settings; only turning the broker on, or
// changing its credentials, needs both.
func TestAnOldBuiltinBrokerWithoutCredentialsStillSavesOtherSettings(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionConfigureResource: true}}
	m := guardedModule(t, "user:manager", access)
	m.setHostConfig(hostSettings{}, wirerpc.Grants{
		Listen: map[string]wirerpc.ListenGrant{builtinListenName: {Network: "tcp", Port: freeTCPPort(t)}},
	})
	old := normalizeSettings(Settings{MQTTBuiltin: true})
	m.held.Store(&old)

	rec := serve(m, "PUT", "/settings", `{"mqtt_builtin":true,"auto_off_cooldown_sec":300}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := m.settings(); got.AutoOffCooldownSec != 300 || !got.MQTTBuiltin {
		t.Fatalf("settings not saved: %+v", got)
	}
	if resp := m.buildSettingsResponse(); resp.MQTTBuiltinRunning || !strings.Contains(resp.MQTTError, "user name and a password") {
		t.Fatalf("the broker should stay off and say why: running=%v error=%q", resp.MQTTBuiltinRunning, resp.MQTTError)
	}

	// Setting only a user name is a change, and is refused without a password.
	rec = serve(m, "PUT", "/settings", `{"mqtt_builtin":true,"mqtt_username":"plugs"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestTheHistoryKeepsTheLastHour(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	var history []PowerSample
	for at := now.Add(-90 * time.Minute); !at.After(now); at = at.Add(30 * time.Second) {
		history = append(history, PowerSample{Timestamp: at.Unix(), Watts: 1})
	}
	kept := trimHistory(history, now, powerHistoryKeep)
	if first := time.Unix(kept[0].Timestamp, 0); now.Sub(first) > powerHistoryKeep {
		t.Fatalf("kept a sample %v old", now.Sub(first))
	}
	if kept[len(kept)-1].Timestamp != now.Unix() {
		t.Fatal("the newest sample was dropped")
	}
	if got := trimHistory(history, now, statusHistoryWindow); len(got) != 61 {
		t.Fatalf("30 minutes at one sample each 30 s is 61 samples, got %d", len(got))
	}
}

func TestTheOverviewCarriesTheGraphOnlyWhenAsked(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	m.mu.Lock()
	m.configs["printer-a"] = DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug_a"}
	m.usage["printer-a"] = &DeviceUsage{History: []PowerSample{
		{Timestamp: now.Add(-2 * time.Hour).Unix(), Watts: 5},
		{Timestamp: now.Add(-40 * time.Minute).Unix(), Watts: 80},
		{Timestamp: now.Unix(), Watts: 120},
	}}
	m.mu.Unlock()
	everyone := func(string) bool { return true }

	if plain := m.buildOverview(overviewOptions{visible: everyone}); plain.Devices[0].PowerHistory != nil {
		t.Fatal("the overview carried the graph without being asked")
	}
	hour := m.buildOverview(overviewOptions{visible: everyone, history: historyAsked("60")})
	if got := *hour.Devices[0].PowerHistory; len(got) != 2 || got[0].Watts != 80 {
		t.Fatalf("history = %+v, want the two samples of the last hour", got)
	}
	quarter := m.buildOverview(overviewOptions{visible: everyone, history: historyAsked("15")})
	if got := *quarter.Devices[0].PowerHistory; len(got) != 1 || got[0].Watts != 120 {
		t.Fatalf("history = %+v, want the one sample of the last 15 minutes", got)
	}

	// A printer with no samples yet answers an empty list, so a graph knows
	// it was asked and is only quiet.
	m.mu.Lock()
	m.configs["printer-b"] = DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug_b"}
	m.mu.Unlock()
	raw, err := json.Marshal(m.buildOverview(overviewOptions{visible: everyone, history: historyAsked("5")}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"device_id":"printer-b","config"`) || strings.Count(string(raw), `"power_history":`) != 2 {
		t.Fatalf("each printer asked about should carry power_history: %s", raw)
	}
}

func TestHistoryAskedIsClamped(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"": 0, "0": 0, "-5": 0, "x": 0, "1": time.Minute, "15": 15 * time.Minute, "600": powerHistoryKeep,
	} {
		if got := historyAsked(raw); got != want {
			t.Errorf("historyAsked(%q) = %v, want %v", raw, got, want)
		}
	}
}
