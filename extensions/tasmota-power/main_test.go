package main

import (
	"testing"
	"time"
)

func TestNormalizeTopicStripsPrefixAndPowerSuffix(t *testing.T) {
	cases := map[string]string{
		"tasmota_FA3578":             "tasmota_FA3578",
		"/tasmota_FA3578/":           "tasmota_FA3578",
		"cmnd/tasmota_FA3578/Power":  "tasmota_FA3578",
		"cmnd/tasmota_FA3578/POWER2": "tasmota_FA3578",
		"stat/Entertainment_Center/": "Entertainment_Center",
	}
	for in, want := range cases {
		if got := normalizeTopic(in); got != want {
			t.Errorf("normalizeTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTasmotaDiscoveryAndMultiRelay(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	discPayload := `{
		"ip":"192.168.178.14",
		"dn":"Entertainment Center",
		"fn":["Printer 1","Printer 2",null,null],
		"hn":"tasmota-14",
		"mac":"AABBCCDDEEFF",
		"md":"NOUS A1T",
		"t":"Entertainment_Center",
		"ft":"%prefix%/%topic%/",
		"tp":["cmnd","stat","tele"],
		"rl":[1,1,0,0,0,0,0,0],
		"state":["OFF","ON","TOGGLE","HOLD"],
		"onln":"Online",
		"ofln":"Offline"
	}`

	customSubs, queryTopic := m.handleMQTTPacket("tasmota/discovery/AABBCCDDEEFF/config", []byte(discPayload), now)
	if len(customSubs) != 0 {
		t.Fatalf("expected no custom subs for default FullTopic, got %v", customSubs)
	}
	if queryTopic != "Entertainment_Center" {
		t.Fatalf("expected queryTopic Entertainment_Center, got %q", queryTopic)
	}

	m.mu.RLock()
	plug := m.plugs["Entertainment_Center"]
	m.mu.RUnlock()
	if plug == nil {
		t.Fatal("expected discovered plug to be stored")
	}
	if plug.FriendlyName != "Printer 1" {
		t.Errorf("FriendlyName = %q, want Printer 1", plug.FriendlyName)
	}
	if plug.Model != "NOUS A1T" {
		t.Errorf("Model = %q, want NOUS A1T", plug.Model)
	}
	if plug.RelayCount != 2 {
		t.Errorf("RelayCount = %d, want 2", plug.RelayCount)
	}
	if len(plug.RelayNames) != 2 || plug.RelayNames[1] != "Printer 2" {
		t.Errorf("RelayNames = %v, want [Printer 1 Printer 2]", plug.RelayNames)
	}
}

func TestTasmotaSingleAndSetOption26(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	m.handleMQTTPacket("stat/plug1/RESULT", []byte(`{"POWER":"ON"}`), now)
	m.mu.RLock()
	st1 := m.plugs["plug1"].relayState(1)
	m.mu.RUnlock()
	if st1 != "ON" {
		t.Fatalf("relay 1 after POWER=ON got %q, want ON", st1)
	}

	m.handleMQTTPacket("stat/plug1/RESULT", []byte(`{"POWER1":"OFF"}`), now)
	m.mu.RLock()
	st1 = m.plugs["plug1"].relayState(1)
	powerField := m.plugs["plug1"].Power
	m.mu.RUnlock()
	if st1 != "OFF" || powerField != "OFF" {
		t.Fatalf("relay 1 after SetOption26 POWER1=OFF got (%q, %q), want OFF", st1, powerField)
	}
}

func TestTasmotaMultiRelayIsolation(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	m.handleMQTTPacket("stat/strip1/RESULT", []byte(`{"POWER1":"ON"}`), now)
	m.handleMQTTPacket("stat/strip1/POWER2", []byte(`OFF`), now)
	// Ensure Status.Power bitstring never corrupts relay state.
	m.handleMQTTPacket("stat/strip1/STATUS", []byte(`{"Status":{"Power":"00"}}`), now)

	m.mu.RLock()
	p := m.plugs["strip1"]
	r1 := p.relayState(1)
	r2 := p.relayState(2)
	m.mu.RUnlock()

	if r1 != "ON" {
		t.Errorf("relay 1 = %q, want ON", r1)
	}
	if r2 != "OFF" {
		t.Errorf("relay 2 = %q, want OFF", r2)
	}
}

func TestTasmotaSetOption4AndSetOption90(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	// SetOption4 1 puts JSON on stat/<topic>/POWER1 directly.
	m.handleMQTTPacket("stat/so4plug/POWER1", []byte(`{"POWER1":"ON"}`), now)
	// SetOption90 1 sends only RESULT JSON.
	m.handleMQTTPacket("stat/so4plug/RESULT", []byte(`{"POWER1":"OFF"}`), now)
	// SetOption4 1 single-relay default sends JSON then plain text on POWER.
	m.handleMQTTPacket("stat/so4plug/POWER", []byte(`{"POWER":"ON"}`), now)

	m.mu.RLock()
	got := m.plugs["so4plug"].relayState(1)
	m.mu.RUnlock()
	if got != "ON" {
		t.Fatalf("expected ON, got %q", got)
	}
}

func TestTasmotaCustomStateTextAndEmptyPayload(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	disc := `{"t":"deplug","ft":"%prefix%/%topic%/","tp":["cmnd","stat","tele"],"rl":[1],"state":["aus","an","TOGGLE","HOLD"]}`
	m.handleMQTTPacket("tasmota/discovery/112233445566/config", []byte(disc), now)

	m.handleMQTTPacket("stat/deplug/RESULT", []byte(`{"POWER":"an"}`), now)
	m.mu.RLock()
	if got := m.plugs["deplug"].relayState(1); got != "ON" {
		m.mu.RUnlock()
		t.Fatalf("custom StateText 'an' -> %q, want ON", got)
	}
	m.mu.RUnlock()

	// Empty payload from PowerRetain 0 must be ignored and keep ON.
	m.handleMQTTPacket("stat/deplug/POWER", []byte(""), now)
	m.mu.RLock()
	if got := m.plugs["deplug"].relayState(1); got != "ON" {
		m.mu.RUnlock()
		t.Fatalf("empty payload changed state to %q, want ON", got)
	}
	m.mu.RUnlock()

	m.handleMQTTPacket("stat/deplug/POWER1", []byte("aus"), now)
	m.mu.RLock()
	if got := m.plugs["deplug"].relayState(1); got != "OFF" {
		m.mu.RUnlock()
		t.Fatalf("custom StateText 'aus' -> %q, want OFF", got)
	}
	m.mu.RUnlock()
}

func TestTasmotaCustomFullTopicAndStaleLWT(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	// Stale retained Offline on old default LWT topic arrives first.
	m.handleMQTTPacket("tele/Entertainment_Center/LWT", []byte("Offline"), now)

	// Discovery announces custom FullTopic & Prefix2=state, Prefix3=telem.
	disc := `{
		"dn":"Entertainment Center",
		"t":"Entertainment_Center",
		"ft":"devintest/%topic%/%prefix%/",
		"tp":["cmnd","state","telem"],
		"rl":[1],
		"onln":"Connected",
		"ofln":"Disconnected"
	}`
	customSubs, _ := m.handleMQTTPacket("tasmota/discovery/AABBCCDDEEFF/config", []byte(disc), now)
	if len(customSubs) != 2 || customSubs[0] != "devintest/Entertainment_Center/state/+" || customSubs[1] != "devintest/Entertainment_Center/telem/+" {
		t.Fatalf("unexpected customSubs: %v", customSubs)
	}

	// Verify command topic resolution.
	m.mu.RLock()
	plug := m.plugs["Entertainment_Center"]
	cmdTopic := plug.formatTopic(0, "POWER1")
	m.mu.RUnlock()
	if cmdTopic != "devintest/Entertainment_Center/cmnd/POWER1" {
		t.Fatalf("formatTopic = %q", cmdTopic)
	}

	// Custom LWT Online sets Online=true and asks to query state.
	_, qt := m.handleMQTTPacket("devintest/Entertainment_Center/telem/LWT", []byte("Connected"), now)
	if qt != "Entertainment_Center" {
		t.Fatalf("expected queryTopic on LWT Online transition, got %q", qt)
	}

	// Subsequent stale Offline on old tele/<topic>/LWT must be ignored.
	m.handleMQTTPacket("tele/Entertainment_Center/LWT", []byte("Offline"), now)

	// State message arrives under custom layout.
	m.handleMQTTPacket("devintest/Entertainment_Center/state/RESULT", []byte(`{"POWER":"ON"}`), now)

	m.mu.RLock()
	online := m.plugs["Entertainment_Center"].Online
	power := m.plugs["Entertainment_Center"].relayState(1)
	m.mu.RUnlock()
	if online == nil || !*online {
		t.Fatalf("expected Online=true (stale LWT ignored), got %v", online)
	}
	if power != "ON" {
		t.Fatalf("expected power=ON, got %q", power)
	}
}

func TestTasmotaCommandErrorWaiterAndEnergyStatus(t *testing.T) {
	m := newModule(nil)
	now := time.Now()

	waiter := &stateWaiter{
		topic: "plugX",
		relay: 3,
		ch:    make(chan stateEvent, 4),
	}
	m.mu.Lock()
	m.waiters = append(m.waiters, waiter)
	m.mu.Unlock()
	defer m.removeWaiter(waiter)

	m.handleMQTTPacket("stat/plugX/RESULT", []byte(`{"Command":"Error","Input":"POWER3 1"}`), now)

	select {
	case evt := <-waiter.ch:
		if evt.Err == "" {
			t.Fatal("expected non-empty error event on Command: Error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Command: Error waiter notification")
	}

	m.handleMQTTPacket("stat/plugX/STATUS10", []byte(`{"StatusSNS":{"ENERGY":{"Power":124.5,"Voltage":231,"Current":0.54}}}`), now)
	m.handleMQTTPacket("stat/plugX/STATUS11", []byte(`{"StatusSTS":{"POWER":"ON"}}`), now)

	m.mu.RLock()
	plug := m.plugs["plugX"]
	m.mu.RUnlock()
	if plug.PowerWatts == nil || *plug.PowerWatts != 124.5 {
		t.Errorf("PowerWatts = %v, want 124.5", plug.PowerWatts)
	}
	if plug.relayState(1) != "ON" {
		t.Errorf("relay 1 after STATUS11 = %q, want ON", plug.relayState(1))
	}
}
