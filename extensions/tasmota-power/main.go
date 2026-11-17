package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/stateapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const (
	name    = "tasmota-power"
	release = extension.Version("0.1.0")

	defaultCooldownSec = 120
	stateKeyPrefix     = "device/"
)

//go:embed openapi.json
var openAPI []byte

// Settings mirrors the options block in module.yaml.
type Settings struct {
	MQTTBroker         string `json:"mqtt_broker"`
	MQTTUsername       string `json:"mqtt_username"`
	MQTTPassword       string `json:"mqtt_password"`
	MQTTClientID       string `json:"mqtt_client_id"`
	AutoOffCooldownSec int    `json:"auto_off_cooldown_sec"`
}

// DeviceConfig is persisted per printer in stateapi.
type DeviceConfig struct {
	TasmotaTopic      string `json:"tasmota_topic"`
	AutoOffAfterPrint bool   `json:"auto_off_after_print"`
	CooldownSec       *int   `json:"cooldown_sec,omitempty"`
}

// PlugTelemetry holds live MQTT state for a Tasmota topic.
type PlugTelemetry struct {
	Power       string     `json:"power"`        // "ON", "OFF", or "UNKNOWN"
	Online      *bool      `json:"online"`       // from tele/<topic>/LWT
	PowerWatts  *float64   `json:"power_watts"`  // from tele/<topic>/SENSOR ENERGY.Power
	Voltage     *float64   `json:"voltage"`      // from tele/<topic>/SENSOR ENERGY.Voltage
	CurrentAmps *float64   `json:"current_amps"` // from tele/<topic>/SENSOR ENERGY.Current
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type autoOffTracker struct {
	sawActive  bool
	finishedAt *time.Time
}

type module struct {
	devices *deviceapi.Client
	state   *stateapi.Client

	held atomic.Pointer[Settings]

	mu         sync.RWMutex
	mqttClient mqtt.Client
	mqttUp     bool
	plugs      map[string]*PlugTelemetry
	configs    map[string]DeviceConfig
	trackers   map[string]*autoOffTracker
}

func newModule(conn *wirerpc.Conn) *module {
	m := &module{
		devices:  deviceapi.NewClient(conn),
		state:    stateapi.NewClient(conn),
		plugs:    make(map[string]*PlugTelemetry),
		configs:  make(map[string]DeviceConfig),
		trackers: make(map[string]*autoOffTracker),
	}
	def := Settings{
		MQTTClientID:       "prusa-connect-tasmota",
		AutoOffCooldownSec: defaultCooldownSec,
	}
	m.held.Store(&def)
	return m
}

func (m *module) settings() Settings {
	if s := m.held.Load(); s != nil {
		return *s
	}
	return Settings{
		MQTTClientID:       "prusa-connect-tasmota",
		AutoOffCooldownSec: defaultCooldownSec,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("render handshake: %w", err)
	}
	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}

	var mod *module
	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{
		Role: wirerpc.RoleChild,
		Methods: wirerpc.Methods{
			wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
				return routesapi.Describe(wirerpc.Info{
					Name:     name,
					Version:  release,
					Contract: wirerpc.Contract,
					Serves:   []wirerpc.Method{wirerpc.MethodConfigure},
				}, openAPI)
			},
			wirerpc.MethodConfigure: wirerpc.Configure(func(cctx context.Context, sent Settings) error {
				if sent.MQTTClientID == "" {
					sent.MQTTClientID = "prusa-connect-tasmota"
				}
				if sent.AutoOffCooldownSec < 0 {
					sent.AutoOffCooldownSec = 0
				}
				mod.held.Store(&sent)
				mod.reconnectMQTT(sent)
				return nil
			}),
			wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
				mod.disconnectMQTT()
				return nil, nil
			},
			routesapi.MethodServe: routesapi.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mod.routes().ServeHTTP(w, r)
			})),
		},
	})
	if err != nil {
		return fmt.Errorf("build wirerpc session: %w", err)
	}

	mod = newModule(conn)

	// Load persisted device configs and start background auto-off loop once the pipe is serving.
	go func() {
		// Small delay to ensure host wire session is ready for outgoing calls.
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		mod.loadAllConfigs(ctx)
		mod.autoOffLoop(ctx)
	}()

	fmt.Fprintf(os.Stderr, "%s %s serving on stdio (pid %d)\n", name, release, os.Getpid())
	if err := conn.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// --- State Persistence ---

func (m *module) loadAllConfigs(ctx context.Context) {
	keys, err := m.state.List(ctx, stateapi.Key(stateKeyPrefix))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: list configs: %v\n", name, err)
		return
	}
	for _, k := range keys {
		val, err := m.state.Get(ctx, k)
		if err != nil {
			continue
		}
		var cfg DeviceConfig
		if err := json.Unmarshal(val.Bytes, &cfg); err != nil {
			continue
		}
		deviceID := strings.TrimPrefix(string(k), stateKeyPrefix)
		m.mu.Lock()
		m.configs[deviceID] = cfg
		m.mu.Unlock()
		if cfg.TasmotaTopic != "" {
			m.queryPlugState(cfg.TasmotaTopic)
		}
	}
}

func (m *module) getConfig(ctx context.Context, deviceID string) (DeviceConfig, error) {
	m.mu.RLock()
	cfg, ok := m.configs[deviceID]
	m.mu.RUnlock()
	if ok {
		return cfg, nil
	}
	val, err := m.state.Get(ctx, stateapi.Key(stateKeyPrefix+deviceID))
	if err != nil {
		if errors.Is(err, stateapi.ErrNoRecord) {
			return DeviceConfig{}, nil
		}
		return DeviceConfig{}, err
	}
	if err := json.Unmarshal(val.Bytes, &cfg); err != nil {
		return DeviceConfig{}, err
	}
	m.mu.Lock()
	m.configs[deviceID] = cfg
	m.mu.Unlock()
	return cfg, nil
}

func (m *module) saveConfig(ctx context.Context, deviceID string, cfg DeviceConfig) error {
	cfg.TasmotaTopic = normalizeTopic(cfg.TasmotaTopic)
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err := m.state.Put(ctx, stateapi.Key(stateKeyPrefix+deviceID), raw); err != nil {
		return err
	}
	m.mu.Lock()
	m.configs[deviceID] = cfg
	if !cfg.AutoOffAfterPrint {
		delete(m.trackers, deviceID)
	}
	m.mu.Unlock()
	if cfg.TasmotaTopic != "" {
		m.queryPlugState(cfg.TasmotaTopic)
	}
	return nil
}

func normalizeTopic(t string) string {
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "/")
	t = strings.TrimPrefix(t, "cmnd/")
	t = strings.TrimPrefix(t, "stat/")
	t = strings.TrimPrefix(t, "tele/")
	t = strings.TrimSuffix(t, "/Power")
	t = strings.TrimSuffix(t, "/POWER")
	return t
}

// --- MQTT ---

func (m *module) disconnectMQTT() {
	m.mu.Lock()
	c := m.mqttClient
	m.mqttClient = nil
	m.mqttUp = false
	m.mu.Unlock()
	if c != nil && c.IsConnected() {
		c.Disconnect(250)
	}
}

func (m *module) reconnectMQTT(s Settings) {
	m.disconnectMQTT()
	if strings.TrimSpace(s.MQTTBroker) == "" {
		return
	}

	opts := mqtt.NewClientOptions().
		AddBroker(s.MQTTBroker).
		SetClientID(s.MQTTClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	if s.MQTTUsername != "" {
		opts.SetUsername(s.MQTTUsername)
		opts.SetPassword(s.MQTTPassword)
	}

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		m.mu.Lock()
		m.mqttUp = true
		topics := make([]string, 0, len(m.configs))
		for _, cfg := range m.configs {
			if cfg.TasmotaTopic != "" {
				topics = append(topics, cfg.TasmotaTopic)
			}
		}
		m.mu.Unlock()

		c.Subscribe("stat/+/POWER", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/LWT", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/STATE", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/SENSOR", 0, m.onMQTTMessage)

		for _, t := range topics {
			c.Publish("cmnd/"+t+"/Power", 0, false, "")
		}
		fmt.Fprintf(os.Stderr, "%s: connected to MQTT broker %s\n", name, s.MQTTBroker)
	})

	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		m.mu.Lock()
		m.mqttUp = false
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "%s: MQTT connection lost: %v\n", name, err)
	})

	client := mqtt.NewClient(opts)
	m.mu.Lock()
	m.mqttClient = client
	m.mu.Unlock()

	go func() {
		if tok := client.Connect(); tok.Wait() && tok.Error() != nil {
			fmt.Fprintf(os.Stderr, "%s: initial MQTT connect failed (will retry): %v\n", name, tok.Error())
		}
	}()
}

func (m *module) onMQTTMessage(_ mqtt.Client, msg mqtt.Message) {
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) < 3 {
		return
	}
	prefix := parts[0]
	topic := strings.Join(parts[1:len(parts)-1], "/")
	leaf := parts[len(parts)-1]
	payload := strings.TrimSpace(string(msg.Payload()))
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	plug, ok := m.plugs[topic]
	if !ok {
		plug = &PlugTelemetry{Power: "UNKNOWN"}
		m.plugs[topic] = plug
	}

	switch {
	case prefix == "stat" && strings.EqualFold(leaf, "POWER"):
		up := strings.ToUpper(payload)
		if up == "ON" || up == "OFF" {
			plug.Power = up
			plug.UpdatedAt = &now
		}
	case prefix == "tele" && leaf == "LWT":
		online := strings.EqualFold(payload, "Online")
		plug.Online = &online
		plug.UpdatedAt = &now
	case prefix == "tele" && leaf == "STATE":
		var st struct {
			POWER string `json:"POWER"`
		}
		if json.Unmarshal(msg.Payload(), &st) == nil && st.POWER != "" {
			plug.Power = strings.ToUpper(st.POWER)
			plug.UpdatedAt = &now
		}
	case prefix == "tele" && leaf == "SENSOR":
		var sens struct {
			ENERGY *struct {
				Power   *float64 `json:"Power"`
				Voltage *float64 `json:"Voltage"`
				Current *float64 `json:"Current"`
			} `json:"ENERGY"`
		}
		if json.Unmarshal(msg.Payload(), &sens) == nil && sens.ENERGY != nil {
			plug.PowerWatts = sens.ENERGY.Power
			plug.Voltage = sens.ENERGY.Voltage
			plug.CurrentAmps = sens.ENERGY.Current
			plug.UpdatedAt = &now
		}
	}
}

func (m *module) queryPlugState(topic string) {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c != nil && up && topic != "" {
		c.Publish("cmnd/"+topic+"/Power", 0, false, "")
	}
}

func (m *module) publishPower(topic, action string) error {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c == nil || !up {
		return errors.New("mqtt broker not connected")
	}
	tok := c.Publish("cmnd/"+topic+"/Power", 0, false, strings.ToUpper(action))
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}

// --- Printer Activity & Safety Interlock ---

type printerSafety struct {
	Connected    bool
	PrinterState string
	ActiveJob    bool
	CanTurnOff   bool
	LockReason   string
}

func (m *module) checkSafety(ctx context.Context, principal, deviceID string) (printerSafety, error) {
	dev, err := m.devices.Get(ctx, deviceapi.GetParams{Principal: principal, ID: deviceID})
	if err != nil {
		return printerSafety{}, err
	}

	var stateStr string
	if raw, ok := dev.State["/state"]; ok {
		_ = json.Unmarshal(raw, &stateStr)
	}
	stateUpper := strings.ToUpper(strings.TrimSpace(stateStr))

	jobs, err := m.devices.Jobs(ctx, deviceapi.JobsParams{Principal: principal, ID: deviceID})
	activeJob := err == nil && jobs.Current != nil && jobs.Current.Ended == nil

	s := printerSafety{
		Connected:    dev.Connected,
		PrinterState: stateUpper,
		ActiveJob:    activeJob,
		CanTurnOff:   true,
	}

	if !dev.Connected {
		return s, nil
	}

	switch stateUpper {
	case "PRINTING", "PAUSED", "BUSY", "ATTENTION":
		s.CanTurnOff = false
		s.LockReason = fmt.Sprintf("Printer is currently %s", strings.ToLower(stateUpper))
		return s, nil
	}

	if activeJob {
		s.CanTurnOff = false
		s.LockReason = "Printer has an active print job"
		return s, nil
	}

	return s, nil
}

// --- Background Auto-Off Loop ---

func (m *module) autoOffLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.stepAutoOff(ctx)
		}
	}
}

func (m *module) stepAutoOff(ctx context.Context) {
	m.mu.RLock()
	armed := make(map[string]DeviceConfig)
	for id, cfg := range m.configs {
		if cfg.AutoOffAfterPrint && cfg.TasmotaTopic != "" {
			armed[id] = cfg
		}
	}
	m.mu.RUnlock()

	if len(armed) == 0 {
		return
	}

	defaultCooldown := m.settings().AutoOffCooldownSec

	for deviceID, cfg := range armed {
		safety, err := m.checkSafety(ctx, "", deviceID)
		if err != nil {
			continue
		}

		cooldownSec := defaultCooldown
		if cfg.CooldownSec != nil {
			cooldownSec = *cfg.CooldownSec
		}

		m.mu.Lock()
		tr, ok := m.trackers[deviceID]
		if !ok {
			tr = &autoOffTracker{}
			m.trackers[deviceID] = tr
		}

		if !safety.CanTurnOff {
			// Printer is actively printing/busy; arm transition detection and clear any old finish timer.
			tr.sawActive = true
			tr.finishedAt = nil
			m.mu.Unlock()
			continue
		}

		// Printer is idle/finished. Only auto-off if we previously observed it active (or it is FINISHED).
		if !tr.sawActive && safety.PrinterState != "FINISHED" {
			m.mu.Unlock()
			continue
		}

		if tr.finishedAt == nil {
			now := time.Now()
			tr.finishedAt = &now
		}

		elapsed := time.Since(*tr.finishedAt)
		ready := elapsed >= time.Duration(cooldownSec)*time.Second
		m.mu.Unlock()

		if !ready {
			continue
		}

		// Re-check safety right before cutting power.
		recheck, err := m.checkSafety(ctx, "", deviceID)
		if err != nil || !recheck.CanTurnOff {
			continue
		}

		if err := m.publishPower(cfg.TasmotaTopic, "OFF"); err != nil {
			fmt.Fprintf(os.Stderr, "%s: auto-off publish failed for %s: %v\n", name, deviceID, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "%s: auto-off triggered for device %s (topic %s)\n", name, deviceID, cfg.TasmotaTopic)
		cfg.AutoOffAfterPrint = false
		_ = m.saveConfig(ctx, deviceID, cfg)
	}
}

// --- HTTP Routes ---

type statusResponse struct {
	DeviceID             string         `json:"device_id"`
	MQTTConfigured       bool           `json:"mqtt_configured"`
	MQTTConnected        bool           `json:"mqtt_connected"`
	Config               DeviceConfig   `json:"config"`
	Plug                 *PlugTelemetry `json:"plug,omitempty"`
	CanTurnOff           bool           `json:"can_turn_off"`
	LockReason           string         `json:"lock_reason,omitempty"`
	PrinterConnected     bool           `json:"printer_connected"`
	PrinterState         string         `json:"printer_state,omitempty"`
	AutoOffPending       bool           `json:"auto_off_pending"`
	AutoOffRemainingSec  int            `json:"auto_off_remaining_sec"`
	DefaultCooldownSec   int            `json:"default_cooldown_sec"`
}

func (m *module) buildStatus(ctx context.Context, principal, deviceID string) (statusResponse, error) {
	cfg, err := m.getConfig(ctx, deviceID)
	if err != nil {
		return statusResponse{}, err
	}
	safety, err := m.checkSafety(ctx, principal, deviceID)
	if err != nil {
		return statusResponse{}, err
	}

	s := m.settings()
	cooldownSec := s.AutoOffCooldownSec
	if cfg.CooldownSec != nil {
		cooldownSec = *cfg.CooldownSec
	}

	m.mu.RLock()
	mqttUp := m.mqttUp
	var plugCopy *PlugTelemetry
	if cfg.TasmotaTopic != "" {
		if p, ok := m.plugs[cfg.TasmotaTopic]; ok {
			cp := *p
			plugCopy = &cp
		} else {
			plugCopy = &PlugTelemetry{Power: "UNKNOWN"}
		}
	}
	var pending bool
	var remaining int
	if cfg.AutoOffAfterPrint {
		if tr, ok := m.trackers[deviceID]; ok && tr.finishedAt != nil && safety.CanTurnOff {
			pending = true
			rem := int((time.Duration(cooldownSec)*time.Second - time.Since(*tr.finishedAt)).Seconds())
			if rem > 0 {
				remaining = rem
			}
		}
	}
	m.mu.RUnlock()

	return statusResponse{
		DeviceID:            deviceID,
		MQTTConfigured:      strings.TrimSpace(s.MQTTBroker) != "",
		MQTTConnected:       mqttUp,
		Config:              cfg,
		Plug:                plugCopy,
		CanTurnOff:          safety.CanTurnOff,
		LockReason:          safety.LockReason,
		PrinterConnected:    safety.Connected,
		PrinterState:        safety.PrinterState,
		AutoOffPending:      pending,
		AutoOffRemainingSec: remaining,
		DefaultCooldownSec:  s.AutoOffCooldownSec,
	}, nil
}

func (m *module) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /devices/{device_id}", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("PUT /devices/{device_id}/config", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body DeviceConfig
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		if err := m.saveConfig(r.Context(), deviceID, body); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /devices/{device_id}/power", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body struct {
			Action string `json:"action"` // "on", "off", "toggle"
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		action := strings.ToLower(strings.TrimSpace(body.Action))
		if action != "on" && action != "off" && action != "toggle" {
			refuse(w, r, InvalidInput.New("action must be on, off, or toggle"))
			return
		}

		cfg, err := m.getConfig(r.Context(), deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		if cfg.TasmotaTopic == "" {
			refuse(w, r, NotConfigured.New("no Tasmota topic configured for this printer"))
			return
		}

		// Resolve toggle against current known state.
		if action == "toggle" {
			m.mu.RLock()
			p := m.plugs[cfg.TasmotaTopic]
			cur := ""
			if p != nil {
				cur = p.Power
			}
			m.mu.RUnlock()
			if cur == "ON" {
				action = "off"
			} else {
				action = "on"
			}
		}

		// Enforce active-print safety interlock before turning OFF.
		if action == "off" {
			safety, err := m.checkSafety(r.Context(), principal, deviceID)
			if err != nil {
				refuse(w, r, InternalError.New(err.Error()))
				return
			}
			if !safety.CanTurnOff {
				refuse(w, r, PrinterBusy.New(safety.LockReason).With(fault.StringParam("state", safety.PrinterState)))
				return
			}
		}

		if err := m.publishPower(cfg.TasmotaTopic, action); err != nil {
			refuse(w, r, BrokerOffline.New(err.Error()))
			return
		}

		// Optimistically update local state so UI responds immediately even before stat/ arrives.
		m.mu.Lock()
		p, ok := m.plugs[cfg.TasmotaTopic]
		if !ok {
			p = &PlugTelemetry{}
			m.plugs[cfg.TasmotaTopic] = p
		}
		p.Power = strings.ToUpper(action)
		now := time.Now()
		p.UpdatedAt = &now
		m.mu.Unlock()

		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /devices/{device_id}/auto-off", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}

		cfg, err := m.getConfig(r.Context(), deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		if cfg.TasmotaTopic == "" {
			refuse(w, r, NotConfigured.New("no Tasmota topic configured for this printer"))
			return
		}
		cfg.AutoOffAfterPrint = body.Enabled
		if err := m.saveConfig(r.Context(), deviceID, cfg); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}

		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		refuse(w, r, RouteNotFound.New("unknown path").With(fault.StringParam("path", r.URL.Path)))
	})

	return mux
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
