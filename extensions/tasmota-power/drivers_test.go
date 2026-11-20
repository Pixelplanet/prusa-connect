package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func TestLegacyModeBackwardsCompatibility(t *testing.T) {
	legacyMQTT := normalizeDeviceConfig(DeviceConfig{
		Mode:         "mqtt",
		TasmotaTopic: "prusa-mk4",
	})
	if legacyMQTT.effectiveDriver() != DriverTasmota || legacyMQTT.plugKey() != "prusa-mk4" {
		t.Fatalf("unexpected legacy MQTT config: driver=%q key=%q", legacyMQTT.effectiveDriver(), legacyMQTT.plugKey())
	}

	legacyHTTP := normalizeDeviceConfig(DeviceConfig{
		Mode:     "http",
		HTTPHost: "192.168.1.50",
	})
	if legacyHTTP.effectiveDriver() != DriverTasmotaHTTP || legacyHTTP.plugKey() != "http:192.168.1.50" {
		t.Fatalf("unexpected legacy HTTP config: driver=%q key=%q", legacyHTTP.effectiveDriver(), legacyHTTP.plugKey())
	}
}

func TestShellyGen1MQTTDriver(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	if !m.handleDriverMQTTPacket("shellies/announce", []byte(`{"id":"shellyplug-s-123456","model":"SHPLG-S"}`), now) {
		t.Fatal("expected shellies/announce to be handled")
	}
	if !m.handleDriverMQTTPacket("shellies/shellyplug-s-123456/online", []byte("true"), now) {
		t.Fatal("expected shelly online to be handled")
	}
	if !m.handleDriverMQTTPacket("shellies/shellyplug-s-123456/relay/0", []byte("on"), now) {
		t.Fatal("expected shelly relay/0 to be handled")
	}
	if !m.handleDriverMQTTPacket("shellies/shellyplug-s-123456/relay/0/power", []byte("142.5"), now) {
		t.Fatal("expected shelly power to be handled")
	}

	m.mu.RLock()
	plug := m.plugs["shelly1:shellyplug-s-123456"]
	discovered := m.discoveredListLocked()
	m.mu.RUnlock()

	if plug == nil {
		t.Fatal("expected shelly1 plug telemetry")
	}
	if plug.Model != "SHPLG-S" || plug.relayState(1) != "ON" || plug.PowerWatts == nil || *plug.PowerWatts != 142.5 {
		t.Fatalf("unexpected shelly1 telemetry: %+v", plug)
	}
	if len(discovered) != 1 || discovered[0].Driver != DriverShellyGen1 || discovered[0].Topic != "shellyplug-s-123456" {
		t.Fatalf("unexpected discovered list: %+v", discovered)
	}

	// Overpower protection
	m.handleDriverMQTTPacket("shellies/shellyplug-s-123456/relay/0", []byte("overpower"), now)
	m.mu.RLock()
	if plug.relayState(1) != "OFF" || !strings.Contains(plug.LastError, "overpower") {
		t.Fatalf("expected overpower to set OFF + error, got state=%q err=%q", plug.relayState(1), plug.LastError)
	}
	m.mu.RUnlock()
}

func TestShellyGen2MQTTDriver(t *testing.T) {
	m := newModule(nil)
	cfg := normalizeDeviceConfig(DeviceConfig{
		Driver:       DriverShellyGen2,
		TasmotaTopic: "shellyplusplugs-abc123",
		RelayIndex:   1,
	})
	m.mu.Lock()
	m.configs["dev-1"] = cfg
	m.mu.Unlock()

	now := time.Now()
	if !m.handleDriverMQTTPacket("shellyplusplugs-abc123/online", []byte("true"), now) {
		t.Fatal("expected shelly gen2 online to be handled")
	}
	if !m.handleDriverMQTTPacket("shellyplusplugs-abc123/status/switch:0", []byte(`{"id":0,"output":true,"apower":210.4,"voltage":230.2,"current":0.92}`), now) {
		t.Fatal("expected shelly gen2 status/switch:0 to be handled")
	}

	m.mu.RLock()
	plug := m.plugs[cfg.plugKey()]
	m.mu.RUnlock()
	if plug == nil || plug.relayState(1) != "ON" || plug.PowerWatts == nil || *plug.PowerWatts != 210.4 || plug.Voltage == nil || *plug.Voltage != 230.2 {
		t.Fatalf("unexpected shelly gen2 telemetry: %+v", plug)
	}
}

func TestZigbee2MQTTDriver(t *testing.T) {
	m := newModule(nil)
	cfg := normalizeDeviceConfig(DeviceConfig{
		Driver:       DriverZigbee2MQTT,
		TasmotaTopic: "printer_plug",
		RelayIndex:   2,
	})
	if cfg.TasmotaTopic != "zigbee2mqtt/printer_plug" {
		t.Fatalf("expected zigbee2mqtt/ prefix, got %q", cfg.TasmotaTopic)
	}
	m.mu.Lock()
	m.configs["dev-z2m"] = cfg
	m.mu.Unlock()

	now := time.Now()
	m.handleDriverMQTTPacket("zigbee2mqtt/printer_plug/availability", []byte(`{"state":"online"}`), now)
	m.handleDriverMQTTPacket("zigbee2mqtt/printer_plug", []byte(`{"state_l2":"ON","power":85.5,"voltage":231,"current":0.38}`), now)

	m.mu.RLock()
	plug := m.plugs[cfg.plugKey()]
	m.mu.RUnlock()
	if plug == nil || plug.Online == nil || !*plug.Online || plug.relayState(2) != "ON" || plug.PowerWatts == nil || *plug.PowerWatts != 85.5 {
		t.Fatalf("unexpected z2m telemetry: %+v", plug)
	}
}

func TestESPHomeAndOpenBekenDrivers(t *testing.T) {
	m := newModule(nil)
	espCfg := normalizeDeviceConfig(DeviceConfig{
		Driver:       DriverESPHome,
		TasmotaTopic: "prusa-plug/switch/relay",
	})
	obkCfg := normalizeDeviceConfig(DeviceConfig{
		Driver:       DriverOpenBeken,
		TasmotaTopic: "obk_printer",
		RelayIndex:   1,
	})
	m.mu.Lock()
	m.configs["dev-esp"] = espCfg
	m.configs["dev-obk"] = obkCfg
	m.mu.Unlock()

	now := time.Now()
	m.handleDriverMQTTPacket("prusa-plug/status", []byte("online"), now)
	m.handleDriverMQTTPacket("prusa-plug/switch/relay/state", []byte("ON"), now)

	m.handleDriverMQTTPacket("obk_printer/connected", []byte("online"), now)
	m.handleDriverMQTTPacket("obk_printer/0/get", []byte("1"), now)
	m.handleDriverMQTTPacket("obk_printer/power/get", []byte("64.2"), now)

	m.mu.RLock()
	espPlug := m.plugs[espCfg.plugKey()]
	obkPlug := m.plugs[obkCfg.plugKey()]
	m.mu.RUnlock()

	if espPlug == nil || espPlug.Online == nil || !*espPlug.Online || espPlug.relayState(1) != "ON" {
		t.Fatalf("unexpected esphome telemetry: %+v", espPlug)
	}
	if obkPlug == nil || obkPlug.Online == nil || !*obkPlug.Online || obkPlug.relayState(1) != "ON" || obkPlug.PowerWatts == nil || *obkPlug.PowerWatts != 64.2 {
		t.Fatalf("unexpected openbeken telemetry: %+v", obkPlug)
	}
}

func TestGenericMQTTDriver(t *testing.T) {
	m := newModule(nil)
	cfg := normalizeDeviceConfig(DeviceConfig{
		Driver:                DriverGenericMQTT,
		MQTTCommandTopic:      "home/printer/set",
		MQTTStateTopic:        "home/printer/state",
		MQTTAvailabilityTopic: "home/printer/avail",
		MQTTPayloadOn:         "TRUE",
		MQTTPayloadOff:        "FALSE",
		MQTTStateJSONKey:      "relay",
		MQTTPowerTopic:        "home/printer/telemetry",
		MQTTPowerJSONKey:      "watts",
	})
	m.mu.Lock()
	m.configs["dev-gen"] = cfg
	m.mu.Unlock()

	now := time.Now()
	m.handleDriverMQTTPacket("home/printer/avail", []byte("online"), now)
	m.handleDriverMQTTPacket("home/printer/state", []byte(`{"relay":"TRUE"}`), now)
	m.handleDriverMQTTPacket("home/printer/telemetry", []byte(`{"watts":123.4}`), now)

	m.mu.RLock()
	plug := m.plugs[cfg.plugKey()]
	m.mu.RUnlock()
	if plug == nil || plug.Online == nil || !*plug.Online || plug.relayState(1) != "ON" || plug.PowerWatts == nil || *plug.PowerWatts != 123.4 {
		t.Fatalf("unexpected generic mqtt telemetry: %+v", plug)
	}
}

func TestShellyHTTPGen2AndGen1Driver(t *testing.T) {
	// 1. Shelly Gen2+ RPC
	gen2On := false
	gen2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rpc/Switch.GetStatus":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 0, "output": gen2On, "apower": 95.5, "voltage": 229.8, "current": 0.42,
			})
		case "/rpc/Switch.Set":
			wasOn := gen2On
			gen2On = r.URL.Query().Get("on") == "true"
			_ = json.NewEncoder(w).Encode(map[string]any{"was_on": wasOn})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gen2Srv.Close()

	m := newModule(nil)
	cfg2 := normalizeDeviceConfig(DeviceConfig{
		Driver:     DriverShellyHTTP,
		HTTPHost:   gen2Srv.URL,
		RelayIndex: 1,
	})
	if err := m.pollShellyHTTPPlug(context.Background(), cfg2); err != nil {
		t.Fatalf("poll gen2: %v", err)
	}
	if err := m.switchShellyHTTPPlugVerified(context.Background(), cfg2, true); err != nil {
		t.Fatalf("switch gen2 on: %v", err)
	}
	m.mu.RLock()
	p2 := m.plugs[cfg2.plugKey()]
	m.mu.RUnlock()
	if p2 == nil || p2.relayState(1) != "ON" || p2.PowerWatts == nil || *p2.PowerWatts != 95.5 {
		t.Fatalf("unexpected gen2 http telemetry: %+v", p2)
	}

	// 2. Shelly Gen1 REST fallback
	gen1On := false
	gen1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"relays": []map[string]any{{"ison": gen1On, "overpower": false}},
				"meters": []map[string]any{{"power": 48.0}},
			})
		case "/relay/0":
			gen1On = r.URL.Query().Get("turn") == "on"
			_ = json.NewEncoder(w).Encode(map[string]any{"ison": gen1On})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gen1Srv.Close()

	cfg1 := normalizeDeviceConfig(DeviceConfig{
		Driver:     DriverShellyHTTP,
		HTTPHost:   gen1Srv.URL,
		RelayIndex: 1,
	})
	if err := m.pollShellyHTTPPlug(context.Background(), cfg1); err != nil {
		t.Fatalf("poll gen1: %v", err)
	}
	if err := m.switchShellyHTTPPlugVerified(context.Background(), cfg1, true); err != nil {
		t.Fatalf("switch gen1 on: %v", err)
	}
	m.mu.RLock()
	p1 := m.plugs[cfg1.plugKey()]
	m.mu.RUnlock()
	if p1 == nil || p1.relayState(1) != "ON" || p1.PowerWatts == nil || *p1.PowerWatts != 48.0 {
		t.Fatalf("unexpected gen1 http telemetry: %+v", p1)
	}
}

func TestHomeAssistantDriver(t *testing.T) {
	swState := "off"
	haSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-ha-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "API running."})
		case "/api/states":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"entity_id": "switch.prusa_xl", "state": swState, "attributes": map[string]any{"friendly_name": "Prusa XL Plug"}},
				{"entity_id": "sensor.prusa_xl_power", "state": "118.5", "attributes": map[string]any{"friendly_name": "Prusa XL Power", "device_class": "power", "unit_of_measurement": "W"}},
				{"entity_id": "light.kitchen", "state": "on", "attributes": map[string]any{"friendly_name": "Kitchen"}},
			})
		case "/api/states/switch.prusa_xl":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entity_id": "switch.prusa_xl", "state": swState, "attributes": map[string]any{"friendly_name": "Prusa XL Plug"},
			})
		case "/api/states/sensor.prusa_xl_power":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entity_id": "sensor.prusa_xl_power", "state": "118.5",
			})
		case "/api/services/homeassistant/turn_on":
			swState = "on"
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/services/homeassistant/turn_off":
			swState = "off"
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer haSrv.Close()

	m := newModule(nil)
	s := normalizeSettings(Settings{
		HAURL:   haSrv.URL,
		HAToken: "test-ha-token",
	})
	m.held.Store(&s)

	ok, _, entities, errMsg := m.testHomeAssistant(context.Background(), nil)
	if !ok || errMsg != "" || len(entities) != 2 {
		t.Fatalf("unexpected testHomeAssistant result: ok=%v err=%q entities=%+v", ok, errMsg, entities)
	}

	cfg := normalizeDeviceConfig(DeviceConfig{
		Driver:          DriverHomeAssistant,
		HAEntityID:      "switch.prusa_xl",
		HAPowerEntityID: "sensor.prusa_xl_power",
	})
	if err := m.pollHomeAssistantPlug(context.Background(), cfg); err != nil {
		t.Fatalf("poll HA: %v", err)
	}
	if err := m.switchHomeAssistantPlugVerified(context.Background(), cfg, true); err != nil {
		t.Fatalf("switch HA on: %v", err)
	}

	m.mu.RLock()
	plug := m.plugs[cfg.plugKey()]
	m.mu.RUnlock()
	if plug == nil || plug.relayState(1) != "ON" || plug.FriendlyName != "Prusa XL Plug" || plug.PowerWatts == nil || *plug.PowerWatts != 118.5 {
		t.Fatalf("unexpected HA plug telemetry: %+v", plug)
	}
}

func TestAllMQTTDriversEndToEndOverRealBroker(t *testing.T) {
	brk, err := StartEmbeddedBroker("127.0.0.1:0", "plugs", "secret")
	if err != nil {
		t.Fatalf("StartEmbeddedBroker: %v", err)
	}
	defer func() { _ = brk.Close() }()

	brokerURL := fmt.Sprintf("tcp://127.0.0.1:%d", brk.Port())

	// Simulated smart-plug responder connected to the real TCP MQTT broker.
	simClient := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetUsername("plugs").
		SetPassword("secret").
		SetClientID("plug-simulator").
		SetConnectTimeout(2 * time.Second).
		SetAutoReconnect(false))
	if tok := simClient.Connect(); !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
		t.Fatalf("simClient connect: %v", tok.Error())
	}
	defer simClient.Disconnect(50)

	type recordedCmd struct {
		Topic   string
		Payload string
	}
	var recMu sync.Mutex
	var recorded []recordedCmd
	record := func(topic, payload string) {
		recMu.Lock()
		recorded = append(recorded, recordedCmd{Topic: topic, Payload: payload})
		recMu.Unlock()
	}

	// Register virtual firmware handlers for all 6 MQTT drivers.
	simSubs := map[string]func(payload string){
		"shellies/shellyplug-sim/relay/0/command": func(p string) {
			record("shellies/shellyplug-sim/relay/0/command", p)
			simClient.Publish("shellies/shellyplug-sim/relay/0", 0, false, p)
			if p == "on" {
				simClient.Publish("shellies/shellyplug-sim/relay/0/power", 0, false, "115.2")
			} else {
				simClient.Publish("shellies/shellyplug-sim/relay/0/power", 0, false, "0")
			}
		},
		"shellyplus-sim/command/switch:0": func(p string) {
			record("shellyplus-sim/command/switch:0", p)
			on := p == "on"
			watts := 0.0
			if on {
				watts = 188.4
			}
			simClient.Publish("shellyplus-sim/status/switch:0", 0, false,
				fmt.Sprintf(`{"id":0,"output":%t,"apower":%.1f,"voltage":230.5,"current":0.82}`, on, watts))
		},
		"zigbee2mqtt/z2m_sim/set": func(p string) {
			record("zigbee2mqtt/z2m_sim/set", p)
			var req map[string]string
			if json.Unmarshal([]byte(p), &req) == nil {
				st := req["state"]
				watts := 0.0
				if st == "ON" {
					watts = 76.0
				}
				simClient.Publish("zigbee2mqtt/z2m_sim", 0, false,
					fmt.Sprintf(`{"state":"%s","power":%.1f}`, st, watts))
			}
		},
		"esphome-sim/switch/printer/command": func(p string) {
			record("esphome-sim/switch/printer/command", p)
			simClient.Publish("esphome-sim/switch/printer/state", 0, false, p)
		},
		"obk_sim/0/set": func(p string) {
			record("obk_sim/0/set", p)
			simClient.Publish("obk_sim/0/get", 0, false, p)
			if p == "1" {
				simClient.Publish("obk_sim/power/get", 0, false, "92.3")
			} else {
				simClient.Publish("obk_sim/power/get", 0, false, "0")
			}
		},
		"custom/plug/cmd": func(p string) {
			record("custom/plug/cmd", p)
			simClient.Publish("custom/plug/stat", 0, false, fmt.Sprintf(`{"relay":"%s"}`, p))
		},
	}

	for topic, handler := range simSubs {
		h := handler
		if tok := simClient.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
			h(string(msg.Payload()))
		}); !tok.WaitTimeout(2*time.Second) || tok.Error() != nil {
			t.Fatalf("simClient subscribe %s: %v", topic, tok.Error())
		}
	}

	// Connect the real extension module to the embedded broker.
	m := newModule(nil)
	s := normalizeSettings(Settings{
		MQTTHost:     "127.0.0.1",
		MQTTPort:     brk.Port(),
		MQTTUsername: "plugs",
		MQTTPassword: "secret",
		MQTTClientID: "ext-e2e-sim",
	})
	modClient := mqtt.NewClient(m.buildMQTTOptions(s, brokerURL, "ext-e2e-sim").SetAutoReconnect(false))
	if tok := modClient.Connect(); !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
		t.Fatalf("modClient connect: %v", tok.Error())
	}
	defer modClient.Disconnect(50)

	cases := []struct {
		name           string
		cfg            DeviceConfig
		wantTopic      string
		wantPayloadOn  string
		wantPayloadOff string
	}{
		{
			name:           "ShellyGen1",
			cfg:            normalizeDeviceConfig(DeviceConfig{Driver: DriverShellyGen1, TasmotaTopic: "shellyplug-sim", RelayIndex: 1}),
			wantTopic:      "shellies/shellyplug-sim/relay/0/command",
			wantPayloadOn:  "on",
			wantPayloadOff: "off",
		},
		{
			name:           "ShellyGen2",
			cfg:            normalizeDeviceConfig(DeviceConfig{Driver: DriverShellyGen2, TasmotaTopic: "shellyplus-sim", RelayIndex: 1}),
			wantTopic:      "shellyplus-sim/command/switch:0",
			wantPayloadOn:  "on",
			wantPayloadOff: "off",
		},
		{
			name:           "Zigbee2MQTT",
			cfg:            normalizeDeviceConfig(DeviceConfig{Driver: DriverZigbee2MQTT, TasmotaTopic: "z2m_sim", RelayIndex: 1}),
			wantTopic:      "zigbee2mqtt/z2m_sim/set",
			wantPayloadOn:  `{"state":"ON"}`,
			wantPayloadOff: `{"state":"OFF"}`,
		},
		{
			name:           "ESPHome",
			cfg:            normalizeDeviceConfig(DeviceConfig{Driver: DriverESPHome, TasmotaTopic: "esphome-sim/switch/printer"}),
			wantTopic:      "esphome-sim/switch/printer/command",
			wantPayloadOn:  "ON",
			wantPayloadOff: "OFF",
		},
		{
			name:           "OpenBeken",
			cfg:            normalizeDeviceConfig(DeviceConfig{Driver: DriverOpenBeken, TasmotaTopic: "obk_sim", RelayIndex: 1}),
			wantTopic:      "obk_sim/0/set",
			wantPayloadOn:  "1",
			wantPayloadOff: "0",
		},
		{
			name: "GenericMQTT",
			cfg: normalizeDeviceConfig(DeviceConfig{
				Driver:           DriverGenericMQTT,
				MQTTCommandTopic: "custom/plug/cmd",
				MQTTStateTopic:   "custom/plug/stat",
				MQTTPayloadOn:    "ENABLE",
				MQTTPayloadOff:   "DISABLE",
				MQTTStateJSONKey: "relay",
			}),
			wantTopic:      "custom/plug/cmd",
			wantPayloadOn:  "ENABLE",
			wantPayloadOff: "DISABLE",
		},
	}

	m.mu.Lock()
	m.mqttClient = modClient
	m.mqttUp = true
	for _, tc := range cases {
		m.configs["dev-"+tc.name] = tc.cfg
	}
	m.mu.Unlock()

	m.syncDriverSubscriptions()
	time.Sleep(150 * time.Millisecond)

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.switchDevicePower(ctx, tc.cfg, true); err != nil {
				t.Fatalf("%s ON failed: %v", tc.name, err)
			}
			m.mu.RLock()
			stOn := m.plugs[tc.cfg.plugKey()].relayState(tc.cfg.relayIndex())
			m.mu.RUnlock()
			if stOn != "ON" {
				t.Fatalf("%s state after ON = %q, want ON", tc.name, stOn)
			}

			if err := m.switchDevicePower(ctx, tc.cfg, false); err != nil {
				t.Fatalf("%s OFF failed: %v", tc.name, err)
			}
			m.mu.RLock()
			stOff := m.plugs[tc.cfg.plugKey()].relayState(tc.cfg.relayIndex())
			m.mu.RUnlock()
			if stOff != "OFF" {
				t.Fatalf("%s state after OFF = %q, want OFF", tc.name, stOff)
			}

			recMu.Lock()
			var sawOn, sawOff bool
			for _, c := range recorded {
				if c.Topic == tc.wantTopic && c.Payload == tc.wantPayloadOn {
					sawOn = true
				}
				if c.Topic == tc.wantTopic && c.Payload == tc.wantPayloadOff {
					sawOff = true
				}
			}
			recMu.Unlock()
			if !sawOn || !sawOff {
				t.Fatalf("%s did not receive expected commands on %s (sawOn=%v sawOff=%v)", tc.name, tc.wantTopic, sawOn, sawOff)
			}
			t.Logf("SIMULATED %s OK: %s received %q -> ON, %q -> OFF", tc.name, tc.wantTopic, tc.wantPayloadOn, tc.wantPayloadOff)
		})
	}
}
